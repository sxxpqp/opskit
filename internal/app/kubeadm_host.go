package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sxxpqp/opskit/internal/k8s"
)

const kubeadmKubeletUnit = `[Unit]
Description=kubelet: The Kubernetes Node Agent
Wants=network-online.target
After=network-online.target
StartLimitIntervalSec=0

[Service]
ExecStart=/usr/local/bin/kubelet
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
`
const kubeadmKubeletDropin = `[Service]
Environment="KUBELET_KUBECONFIG_ARGS=--bootstrap-kubeconfig=/etc/kubernetes/bootstrap-kubelet.conf --kubeconfig=/etc/kubernetes/kubelet.conf"
Environment="KUBELET_CONFIG_ARGS=--config=/var/lib/kubelet/config.yaml"
EnvironmentFile=-/var/lib/kubelet/kubeadm-flags.env
EnvironmentFile=-/etc/default/kubelet
EnvironmentFile=-/etc/sysconfig/kubelet
ExecStart=
ExecStart=/usr/local/bin/kubelet $KUBELET_KUBECONFIG_ARGS $KUBELET_CONFIG_ARGS $KUBELET_KUBEADM_ARGS $KUBELET_EXTRA_ARGS
`

func installKubeadmTools(ctx context.Context, version, criVersion string, bases map[string]string, criURL, downloads string, out, diag io.Writer) error {
	if err := kubeadmFreshNode(); err != nil {
		return err
	}
	for _, name := range []string{"kubeadm", "kubelet", "kubectl", "crictl"} {
		if found, err := exec.LookPath(name); err == nil {
			return fmt.Errorf("已有 %s: %s；此入口不覆盖或升级已有工具", name, found)
		}
		if err := ensureRuntimeAbsent("/usr/local/bin/" + name); err != nil {
			return err
		}
	}
	unitPath := "/etc/systemd/system/kubelet.service"
	dropinPath := "/etc/systemd/system/kubelet.service.d/10-kubeadm.conf"
	for _, p := range []string{unitPath, dropinPath, "/usr/lib/systemd/system/kubelet.service", "/lib/systemd/system/kubelet.service"} {
		if err := ensureRuntimeAbsent(p); err != nil {
			return err
		}
	}
	if _, err := commandOutput(ctx, nil, "systemctl", "cat", "kubelet.service"); err == nil {
		return errors.New("已有 kubelet systemd unit，不覆盖")
	}
	tmp, err := os.MkdirTemp("", "opskit-kubeadm-tools-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := stageKubeadmTools(ctx, version, criVersion, bases, criURL, downloads, tmp); err != nil {
		return err
	}
	// All packages and versions have been checked before writing live binaries.
	for _, name := range []string{"kubeadm", "kubelet", "kubectl", "crictl"} {
		data, err := os.ReadFile(filepath.Join(tmp, name))
		if err != nil {
			return err
		}
		if err = writeRuntimeNew("/usr/local/bin/"+name, data, 0755); err != nil {
			return err
		}
	}
	if err := writeRuntimeNew(unitPath, []byte(kubeadmKubeletUnit), 0644); err != nil {
		return err
	}
	if err := writeRuntimeNew(dropinPath, []byte(kubeadmKubeletDropin), 0644); err != nil {
		return err
	}
	if err := commandRun(ctx, nil, out, diag, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := commandRun(ctx, nil, out, diag, "systemctl", "enable", "kubelet"); err != nil {
		return err
	}
	fmt.Fprintf(out, "安装完成: kubeadm/kubelet/kubectl %s，crictl %s；kubelet 将在 init/join 时启动。\n", version, criVersion)
	return nil
}

func stageKubeadmTools(ctx context.Context, version, criVersion string, bases map[string]string, criURL, downloads, dir string) error {
	for _, name := range []string{"kubeadm", "kubelet", "kubectl", "crictl"} {
		source := criURL
		targetVersion := criVersion
		if name != "crictl" {
			source = fmt.Sprintf("%s/%s/bin/linux/%s/%s", strings.TrimRight(bases[name], "/"), version, runtime.GOARCH, name)
			targetVersion = version
		}
		var binary, checksum bytes.Buffer
		if err := downloadRetained(ctx, source+".sha256", &checksum, downloads, name, targetVersion); err != nil {
			return err
		}
		fields := strings.Fields(checksum.String())
		if len(fields) == 0 || !regexp.MustCompile(`^[a-fA-F0-9]{64}$`).MatchString(fields[0]) {
			return fmt.Errorf("%s 的 SHA-256 文件无效", name)
		}
		if err := downloadRetained(ctx, source, &binary, downloads, name, targetVersion); err != nil {
			return err
		}
		if err := checkRuntimeHash(binary.Bytes(), fields[0]); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		data := binary.Bytes()
		if name == "crictl" {
			files, err := runtimeArchive(data)
			if err != nil {
				return err
			}
			var ok bool
			data, ok = files["crictl"]
			if !ok {
				return errors.New("cri-tools 包缺少 crictl")
			}
		}
		staged := filepath.Join(dir, name)
		if err := os.WriteFile(staged, data, 0700); err != nil {
			return err
		}
		if err := verifyKubeadmTool(ctx, staged, name, targetVersion); err != nil {
			return err
		}
	}
	return nil
}

func verifyKubeadmTool(ctx context.Context, path, name, version string) error {
	if name == "kubectl" {
		return k8s.VerifyKubectl(ctx, path, version)
	}
	args := []string{"--version"}
	if name == "kubeadm" {
		args = []string{"version", "-o", "short"}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := commandOutput(checkCtx, nil, path, args...)
	if err != nil {
		return err
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 || fields[len(fields)-1] != version {
		return fmt.Errorf("%s 版本与目标 %s 不一致", name, version)
	}
	return nil
}

// ensureKubeadmKernelModules loads only missing kernel modules; it does not
// swallow a real load failure, which must block kubeadm preparation.
func ensureKubeadmKernelModules(sysModule string, load func(string) error) error {
	for _, module := range []string{"overlay", "br_netfilter"} {
		if _, err := os.Stat(filepath.Join(sysModule, module)); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("检查内核模块 %s: %w", module, err)
		}
		if err := load(module); err != nil {
			return fmt.Errorf("加载内核模块 %s: %w", module, err)
		}
	}
	return nil
}

func prepareKubeadmHost(ctx context.Context, out, diag io.Writer) error {
	// Verify OS dependencies before touching persistent host settings.
	for _, tool := range []string{"modprobe", "sysctl", "swapoff", "conntrack", "socat", "iptables", "ip", "tc", "mount", "nsenter", "ethtool"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("缺少系统依赖 %s；请先通过发行版软件包管理器安装", tool)
		}
	}
	// Modules built into the kernel or already loaded appear under /sys/module.
	// In minimal containers there may be no matching /lib/modules tree even
	// though the running host kernel already provides the required capability.
	if err := ensureKubeadmKernelModules("/sys/module", func(module string) error {
		return commandRun(ctx, nil, out, diag, "modprobe", module)
	}); err != nil {
		return err
	}
	fstab, err := os.ReadFile("/etc/fstab")
	if err != nil {
		return err
	}
	if err = writeConfigFile("/etc/fstab", []byte(disableFstabSwap(string(fstab))), out); err != nil {
		return err
	}
	if err = writeConfigFile("/etc/modules-load.d/opskit-kubernetes.conf", []byte("overlay\nbr_netfilter\n"), out); err != nil {
		return err
	}
	settings := "net.bridge.bridge-nf-call-iptables = 1\nnet.bridge.bridge-nf-call-ip6tables = 1\nnet.ipv4.ip_forward = 1\n"
	if err = writeConfigFile("/etc/sysctl.d/99-opskit-kubernetes.conf", []byte(settings), out); err != nil {
		return err
	}
	if err = commandRun(ctx, nil, out, diag, "sysctl", "-p", "/etc/sysctl.d/99-opskit-kubernetes.conf"); err != nil {
		return err
	}
	// Calling swapoff -a when no swap is enabled can fail in isolated
	// containers even though there is nothing to disable. Never skip it when
	// /proc/swaps reports active swap devices.
	if err = disableActiveKubeadmSwap("/proc/swaps", func() error {
		return commandRun(ctx, nil, out, diag, "swapoff", "-a")
	}); err != nil {
		return err
	}
	fmt.Fprintln(out, "节点准备完成；systemd swap unit、zram 等非 fstab swap 来源需自行禁用，init/join 会再次检查实际 swap。")
	return nil
}
// disableActiveKubeadmSwap skips swapoff only when the kernel reports no
// active swap. If swap is active, failure to disable it blocks installation.
func disableActiveKubeadmSwap(procSwaps string, swapoff func() error) error {
	data, err := os.ReadFile(procSwaps)
	if err != nil {
		return fmt.Errorf("读取 swap 状态: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 1 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "Filename") {
		return errors.New("/proc/swaps 格式无法识别，拒绝跳过 swap")
	}
	if len(lines) == 1 {
		return nil
	}
	return swapoff()
}

func disableFstabSwap(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[2] == "swap" {
			lines[i] = "# opskit disabled swap: " + line
		}
	}
	return strings.Join(lines, "\n")
}

func checkKubeadmNode(ctx context.Context, version string, minor int, socket string) error {
	for _, name := range []string{"kubeadm", "kubelet", "kubectl"} {
		path, err := exec.LookPath(name)
		if err != nil {
			return err
		}
		if err = verifyKubeadmTool(ctx, path, name, version); err != nil {
			return err
		}
	}
	for _, name := range []string{"crictl", "conntrack", "socat", "iptables", "ip", "tc", "mount", "nsenter", "ethtool"} {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("缺少 %s，请先完成 kubeadm install / prepare 依赖安装", name)
		}
	}
	swap, err := os.ReadFile("/proc/swaps")
	if err != nil {
		return err
	}
	if len(strings.Fields(string(swap))) > 5 {
		return errors.New("swap 仍启用，请先执行 kubeadm prepare 并禁用其他 swap 来源")
	}
	for _, path := range []string{"/proc/sys/net/ipv4/ip_forward", "/proc/sys/net/bridge/bridge-nf-call-iptables", "/proc/sys/net/bridge/bridge-nf-call-ip6tables"} {
		data, err := os.ReadFile(path)
		if err != nil || strings.TrimSpace(string(data)) != "1" {
			return fmt.Errorf("%s 未设置为 1，请先执行 kubeadm prepare", path)
		}
	}
	var raw []byte
	if strings.HasSuffix(socket, "/cri-dockerd.sock") {
		for _, service := range []string{"docker", "cri-docker"} {
			if _, err = commandOutput(ctx, nil, "systemctl", "is-active", "--quiet", service); err != nil {
				return err
			}
		}
		raw, err = commandOutput(ctx, nil, "docker", "info", "--format", "{{.CgroupDriver}}")
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(raw)) != "systemd" {
			return errors.New("Docker 必须配置 systemd cgroup driver")
		}
	} else {
		if _, err = commandOutput(ctx, nil, "systemctl", "is-active", "--quiet", "containerd"); err != nil {
			return fmt.Errorf("containerd 未运行: %w", err)
		}
		raw, err = commandOutput(ctx, nil, "containerd", "--version")
		if err != nil {
			return err
		}
		if err = checkKubeadmContainerd(minor, string(raw)); err != nil {
			return err
		}
		raw, err = commandOutput(ctx, nil, "containerd", "config", "dump")
		if err != nil {
			return err
		}
		if !regexp.MustCompile(`SystemdCgroup\s*=\s*true`).Match(raw) {
			return errors.New("containerd 必须配置 SystemdCgroup = true；不会修改已有运行时")
		}
	}
	// Check RuntimeReady, not NetworkReady: the CNI network is installed after init.
	raw, err = commandOutput(ctx, nil, "crictl", "--timeout=10s", "--runtime-endpoint", socket, "info")
	if err != nil {
		return err
	}
	return validateClusterRuntimeInfo(raw)
}
func checkKubeadmContainerd(k8sMinor int, text string) error {
	match := regexp.MustCompile(`\bv?(\d+)\.(\d+)\.(\d+)\b`).FindStringSubmatch(text)
	if len(match) != 4 {
		return errors.New("无法识别 containerd 版本")
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])
	if major == 1 && (minor == 7 || (minor == 6 && (k8sMinor < 26 || patch >= 15))) {
		return nil
	}
	if major == 2 && k8sMinor >= 26 {
		return nil
	}
	return errors.New("containerd 与 Kubernetes 组合不在支持范围；Kubernetes 1.24/1.25 使用 containerd 1.6/1.7，1.26+ 的 1.6 至少为 1.6.15；不自动降级")
}

func checkKubeadmAdvertiseAddress(address string) error {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return err
	}
	for _, item := range addresses {
		if ipnet, ok := item.(*net.IPNet); ok && ipnet.IP.Equal(net.ParseIP(address)) {
			return nil
		}
	}
	return errors.New("advertise-address 不属于本机网卡")
}

func validateClusterRuntimeInfo(raw []byte) error {
	var info struct {
		Status struct {
			Conditions []struct {
				Type   string `json:"type"`
				Status bool   `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return err
	}
	for _, condition := range info.Status.Conditions {
		if condition.Type == "RuntimeReady" && condition.Status {
			return nil
		}
	}
	return errors.New("containerd CRI 未报告 RuntimeReady=true")
}
