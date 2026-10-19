package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type clusterNodeState struct {
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`
	Cluster     string `json:"cluster"`
	Host        string `json:"host"`
	Setup       bool   `json:"setup"`
	Started     bool   `json:"started"`
	CAHash      string `json:"caHash"`
}

func newK8sNodeCommand(parent context.Context, out, diag io.Writer) (*cobra.Command, error) {
	cmd := &cobra.Command{Use: "node"}
	f := cmd.Flags()
	protocol := f.Bool("protocol", false, "内部节点协议检查")
	root := f.String("bundle", "", "内部部署 bundle")
	phase := f.String("phase", "", "内部部署阶段")
	remaining := f.Duration("timeout", 0, "管理机传入的总期限剩余时间")
	bindCommand(cmd, func() (int, error) {
		if f.NArg() != 0 {
			return 2, errors.New("无效的内部节点参数")
		}
		if *protocol {
			fmt.Fprintln(out, clusterNodeProtocol)
			return 0, nil
		}
		if err := kubeadmHost(); err != nil {
			return 1, err
		}
		raw, err := os.ReadFile(filepath.Join(*root, "plan.json"))
		if err != nil {
			return 1, err
		}
		var plan clusterNodePlan
		if err = json.Unmarshal(raw, &plan); err != nil {
			return 1, err
		}
		if !clusterNamePattern.MatchString(plan.Cluster) || !clusterNamePattern.MatchString(plan.Host) {
			return 2, errors.New("无效部署计划")
		}
		duration, err := time.ParseDuration(plan.Timeout)
		if err != nil || duration <= 0 {
			return 2, errors.New("内部节点超时无效")
		}
		if explicitFlag(f, "timeout") {
			if *remaining <= 0 {
				return 2, errors.New("剩余超时必须大于零")
			}
			if *remaining < duration {
				duration = *remaining
			}
		}
		ctx, cancel := context.WithTimeout(parent, duration)
		defer cancel()
		ctx = context.WithValue(ctx, clusterCacheKey{}, *root)
		configPath := filepath.Join(*root, "node.yaml")
		c, err := readFileConfig(configPath, true)
		if err != nil {
			return 1, err
		}
		c.projectComponents()
		statePath := "/var/lib/opskit/clusters/" + plan.Cluster + "/state.json"
		state := clusterNodeState{Cluster: plan.Cluster, Host: plan.Host}
		if existing, e := os.ReadFile(statePath); e == nil {
			if err = json.Unmarshal(existing, &state); err != nil {
				return 1, err
			}
			if state.Cluster != plan.Cluster || state.Host != plan.Host {
				return 1, errors.New("节点部署标识与目标不一致")
			}
		} else if !os.IsNotExist(e) {
			return 1, e
		}
		if state.Address != "" && state.Address != plan.Address {
			return 1, errors.New("节点 internalAddress 与已有部署不一致，不隐式迁移地址")
		}
		state.Address = plan.Address
		fingerprint := clusterFingerprint(c.K8s)
		if state.Fingerprint != "" && state.Fingerprint != fingerprint {
			return 1, errors.New("节点目标版本/网络与已有部署不一致，不隐式升级或更改集群")
		}
		state.Fingerprint = fingerprint
		save := func() error {
			raw, err := json.Marshal(state)
			if err != nil {
				return err
			}
			return writeClusterNodeState(statePath, raw)
		}
		owned := func() error { return validateClusterNodeOwnership(state, plan.Master) }
		run := func(args ...string) error {
			args = append(args, "--config", configPath)
			code, err := Run(ctx, args, out, diag, "node")
			if err != nil {
				return err
			}
			if code != 0 {
				return fmt.Errorf("节点命令退出码 %d", code)
			}
			return nil
		}
		kube := func(args ...string) ([]byte, error) {
			return commandOutput(ctx, nil, "kubectl", append([]string{"--kubeconfig", "/etc/kubernetes/admin.conf", "--request-timeout=30s"}, args...)...)
		}
		switch *phase {
		case "probe":
			if plan.Calico && !state.Started {
				err = checkNewCalicoNetwork("/etc/cni/net.d")
				if err != nil {
					break
				}
			}
			err = checkKubeadmAdvertiseAddress(plan.Address)
			if err != nil {
				break
			}
			err = owned()
			if err == nil {
				err = probeClusterInstalled(ctx, c)
			}
		case "setup":
			if err = owned(); err != nil {
				break
			}
			if state.Setup {
				err = verifyClusterInstalled(ctx, c)
				break
			}
			if err = run("kubeadm", "prepare"); err != nil {
				break
			}
			if err = setupClusterRuntime(ctx, c, *root, run, out, diag); err != nil {
				break
			}
			all := true
			for _, name := range []string{"kubeadm", "kubelet", "kubectl", "crictl"} {
				if _, e := exec.LookPath(name); e != nil {
					all = false
					break
				}
			}
			if !all {
				if err = run("kubeadm", "install"); err != nil {
					break
				}
			}
			if err = verifyClusterInstalled(ctx, c); err != nil {
				break
			}
			readyCtx, readyCancel := context.WithTimeout(ctx, time.Minute)
			err = waitContainerdReady(readyCtx, time.Second, func(probe context.Context) error {
				raw, e := commandOutput(probe, nil, "crictl", "--timeout=5s", "--runtime-endpoint", clusterCRISocket(c.K8s.Kubernetes.ContainerManager), "info")
				if e != nil {
					return e
				}
				return validateClusterRuntimeInfo(raw)
			})
			readyCancel()
			if err != nil {
				break
			}
			state.Setup = true
			err = save()
		case "init":
			if !plan.Master {
				err = errors.New("不能在 worker 上初始化控制平面")
				break
			}
			if err = owned(); err != nil {
				break
			}
			if _, e := os.Stat("/etc/kubernetes/admin.conf"); e != nil {
				state.Started = true
				if err = save(); err != nil {
					break
				}
				if err = run("kubeadm", "init", "--advertise-address", plan.Address, "--node-name", plan.Host, "--node-ip", plan.Address, "--network-manifest", filepath.Join(*root, "network.yaml")); err != nil {
					break
				}
			} else {
				if _, err = kube("get", "--raw=/readyz"); err != nil {
					break
				}
				raw, err = kube("version", "--output=json")
				if err != nil {
					break
				}
				if err = validateClusterServerVersion(raw, c.K8s.Kubernetes.Version); err != nil {
					break
				}
				if err = commandRun(ctx, nil, out, diag, "kubectl", "--kubeconfig", "/etc/kubernetes/admin.conf", "apply", "-f", filepath.Join(*root, "network.yaml")); err != nil {
					break
				}
			}
			state.CAHash, err = kubeconfigCAHash("/etc/kubernetes/admin.conf")
			if err != nil {
				break
			}
			if err = save(); err != nil {
				break
			}
			if plan.Single {
				raw, err = kube("get", "node", plan.Host, "-o", "json")
				if err != nil {
					break
				}
				patch, e := singleNodeTaintPatch(raw)
				if e != nil {
					err = e
					break
				}
				if len(patch) > 0 {
					_, err = kube("patch", "node", plan.Host, "--type=merge", "-p", string(patch))
				}
			}

		case "kubeconfig":
			if err = owned(); err != nil {
				break
			}
			raw, err = os.ReadFile("/etc/kubernetes/admin.conf")
			if err == nil {
				_, err = out.Write(raw)
			}
		case "credentials":
			if !plan.Master {
				err = errors.New("只有 master 能生成加入信息")
				break
			}
			if err = owned(); err != nil {
				break
			}
			hash, e := kubeconfigCAHash("/etc/kubernetes/admin.conf")
			if e != nil {
				err = e
				break
			}
			random := make([]byte, 11)
			if _, err = rand.Read(random); err != nil {
				break
			}
			token := hex.EncodeToString(random[:3]) + "." + hex.EncodeToString(random[3:])
			raw, err = yaml.Marshal(map[string]any{"apiVersion": kubeadmConfigAPIVersion(kubeadmVersionMinor(c.K8s.Kubernetes.Version)), "kind": "InitConfiguration", "bootstrapTokens": []any{map[string]any{"token": token, "ttl": "30m0s"}}})
			if err != nil {
				break
			}
			path := filepath.Join(*root, "token.yaml")
			if err = bytesFile(path, raw); err != nil {
				break
			}
			_, err = commandOutput(ctx, nil, "kubeadm", "token", "create", "--config", path, "--kubeconfig", "/etc/kubernetes/admin.conf")
			_ = os.Remove(path)
			if err != nil {
				break
			}
			join := clusterJoin{Token: token, CAHash: hash, Endpoint: c.Kubeadm.ControlPlaneEndpoint}
			err = json.NewEncoder(out).Encode(join)
		case "join":
			if plan.Master {
				err = errors.New("不能在 master 上执行 worker join")
				break
			}
			path := filepath.Join(*root, "join.json")
			raw, err = os.ReadFile(path)
			if err != nil {
				break
			}
			defer os.Remove(path)
			var join clusterJoin
			if err = json.Unmarshal(raw, &join); err != nil {
				break
			}
			if err = owned(); err != nil {
				break
			}
			if _, e := os.Stat("/etc/kubernetes/kubelet.conf"); e == nil {
				hash, e := kubeconfigCAHash("/etc/kubernetes/kubelet.conf")
				if e != nil {
					err = e
					break
				}
				if hash != join.CAHash {
					err = errors.New("worker 已加入其他集群，不自动 reset")
					break
				}
				state.CAHash = hash
				err = save()
				break
			}
			state.Started = true
			state.CAHash = join.CAHash
			if err = save(); err != nil {
				break
			}
			err = run("kubeadm", "join", "--endpoint", join.Endpoint, "--token", join.Token, "--discovery-token-ca-cert-hash", join.CAHash, "--node-name", plan.Host, "--node-ip", plan.Address)
			if err == nil {
				err = save()
			}
		case "ready":
			if err = owned(); err != nil {
				break
			}
			if plan.Calico {
				err = waitClusterCalico(ctx, kube, out, c.K8s.Network.Calico.Version)
				if err != nil {
					break
				}
			}
			err = waitClusterNodesReady(ctx, plan.Nodes, func() ([]byte, error) { return kube("get", "nodes", "-o", "json") })
			if err == nil {
				raw, err = kube("get", "nodes", "-o", "wide")
				if err == nil {
					_, err = out.Write(raw)
				}
			}
		case "sphere":
			if !c.K8s.KubeSphere.Enabled {
				break
			}
			if err = owned(); err != nil {
				break
			}
			raw, err = kube("get", "namespace", "kubesphere-system", "--ignore-not-found", "-o", "name")
			if err != nil {
				break
			}
			if len(bytes.TrimSpace(raw)) > 0 {
				err = errors.New("KS 已存在；本入口不覆盖或重复安装，请检查已有安装状态")
				break
			}
			raw, err = commandOutput(ctx, nil, "kubectl", "--kubeconfig", "/etc/kubernetes/admin.conf", "config", "current-context")
			if err != nil {
				break
			}
			err = run("kubesphere", "install", "--kubeconfig", "/etc/kubernetes/admin.conf", "--context", strings.TrimSpace(string(raw)), "--timeout", duration.String())
		default:
			err = errors.New("未知内部节点阶段")
		}
		if err != nil {
			return 1, err
		}
		return 0, nil
	})
	return cmd, nil
}

func writeClusterNodeState(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func validateClusterNodeOwnership(state clusterNodeState, master bool) error {
	path := "/etc/kubernetes/kubelet.conf"
	if master {
		path = "/etc/kubernetes/admin.conf"
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if !state.Started {
		return errors.New("已有未受当前部署管理的集群，不修改")
	}
	actual, err := kubeconfigCAHash(path)
	if err != nil {
		return err
	}
	if state.CAHash != "" && state.CAHash != actual {
		return errors.New("节点实际 CA 与记录不匹配，拒绝修改")
	}
	return nil
}
func kubeconfigCAHash(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var cfg struct {
		Clusters []struct {
			Cluster struct {
				Data string `yaml:"certificate-authority-data"`
				Path string `yaml:"certificate-authority"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
	}
	if err = yaml.Unmarshal(raw, &cfg); err != nil {
		return "", err
	}
	if len(cfg.Clusters) != 1 {
		return "", errors.New("节点 kubeconfig CA 列表无效")
	}
	cluster := cfg.Clusters[0].Cluster
	var ca []byte
	if cluster.Data != "" {
		ca, err = base64.StdEncoding.DecodeString(cluster.Data)
	} else {
		p := cluster.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(path), p)
		}
		ca, err = os.ReadFile(p)
	}
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(ca)
	if block == nil {
		return "", errors.New("CA PEM 无效")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}
func verifyClusterInstalled(ctx context.Context, c fileConfig) error {
	for _, name := range []string{"kubeadm", "kubelet", "kubectl"} {
		path, err := exec.LookPath(name)
		if err != nil {
			return err
		}
		if err = verifyKubeadmTool(ctx, path, name, c.K8s.Kubernetes.Version); err != nil {
			return err
		}
	}
	path, err := exec.LookPath("crictl")
	if err != nil {
		return err
	}
	if err = verifyKubeadmTool(ctx, path, "crictl", c.K8s.CRITools.Version); err != nil {
		return err
	}
	if _, err = os.Stat("/etc/systemd/system/kubelet.service.d/10-kubeadm.conf"); err != nil {
		return errors.New("缺少受支持的 kubelet systemd drop-in")
	}
	name, version := "containerd", c.K8s.Containerd.Version
	if c.K8s.Kubernetes.ContainerManager == "docker" {
		name, version = "dockerd", c.K8s.Docker.Version
	}
	if err = verifyRuntimeVersion(ctx, name, version); err != nil {
		return err
	}
	if c.K8s.Kubernetes.ContainerManager == "containerd" {
		if err = verifyRuntimeVersion(ctx, "runc", c.K8s.Runc.Version); err != nil {
			return err
		}
	} else {
		if err = verifyRuntimeVersion(ctx, "cri-dockerd", c.K8s.CRIDockerd.Version); err != nil {
			return err
		}
	}
	for _, plugin := range []string{"bridge", "loopback"} {
		if _, err = os.Stat(filepath.Join("/opt/cni/bin", plugin)); err != nil {
			return fmt.Errorf("缺少 CNI plugin %s: %w", plugin, err)
		}
	}
	return nil
}
func setupClusterRuntime(ctx context.Context, c fileConfig, bundle string, run func(...string) error, out, diag io.Writer) error {
	manager := c.K8s.Kubernetes.ContainerManager
	tool, version := "containerd", c.K8s.Containerd.Version
	if manager == "docker" {
		tool, version = "docker", c.K8s.Docker.Version
	}
	binary := tool
	if tool == "docker" {
		binary = "dockerd"
	}
	if path, err := exec.LookPath(binary); err == nil {
		if err = verifyRuntimeVersion(ctx, path, version); err != nil {
			return err
		}
	} else {
		args := []string{tool, "install"}
		if tool == "docker" {
			args = append(args, "--method", "binary")
		}
		if err = run(args...); err != nil {
			return err
		}
	}
	if manager == "docker" {
		if _, err := exec.LookPath("cri-dockerd"); err != nil {
			if err = run("cri-dockerd", "install"); err != nil {
				return err
			}
		} else if err = verifyRuntimeVersion(ctx, "cri-dockerd", c.K8s.CRIDockerd.Version); err != nil {
			return err
		}
		if err := installClusterDockerCNI(ctx, c); err != nil {
			return err
		}
		if err := run("mirror", "docker"); err != nil {
			return err
		}
		// Docker and kubelet must use the same cgroup driver.
		raw, err := os.ReadFile("/etc/docker/daemon.json")
		if err != nil {
			return err
		}
		var daemon map[string]any
		if err = json.Unmarshal(raw, &daemon); err != nil {
			return err
		}
		opts := []string{"native.cgroupdriver=systemd"}
		if existing, ok := daemon["exec-opts"].([]any); ok {
			for _, item := range existing {
				if s, ok := item.(string); ok && !strings.HasPrefix(s, "native.cgroupdriver=") {
					opts = append(opts, s)
				}
			}
		}
		daemon["exec-opts"] = opts
		raw, err = json.MarshalIndent(daemon, "", "  ")
		if err != nil {
			return err
		}
		if err = writeConfigFile("/etc/docker/daemon.json", raw, out); err != nil {
			return err
		}
		if err = commandRun(ctx, nil, out, diag, "systemctl", "restart", "docker", "cri-docker"); err != nil {
			return err
		}
	} else {
		if err := run("mirror", "containerd"); err != nil {
			return err
		}

		if err := commandRun(ctx, nil, out, diag, "systemctl", "restart", "containerd"); err != nil {
			return err
		}
	}
	return nil
}
func installClusterDockerCNI(ctx context.Context, c fileConfig) error {
	marker := "/opt/cni/bin/.opskit-version"
	if raw, err := os.ReadFile(marker); err == nil {
		if strings.TrimSpace(string(raw)) != c.K8s.CNIPlugins.Version {
			return errors.New("已有 CNI plugins 版本与目标不一致")
		}
		return nil
	}
	return installClusterCNIArchive(ctx, c, marker)
}

func installClusterCNIArchive(ctx context.Context, c fileConfig, marker string) error {
	var source string
	for _, asset := range clusterAssets(c, runtime.GOARCH) {
		if strings.Contains(asset.URL, "/containernetworking/plugins/") {
			source = asset.URL
			break
		}
	}
	if source == "" {
		return errors.New("缺少 CNI artifact")
	}
	var buf runtimeDownloadBuffer
	if _, err := cachedClusterDownload(ctx, source, &buf); err != nil {
		return err
	}
	files, err := runtimeArchive(buf.Bytes())
	if err != nil {
		return err
	}
	for name := range files {
		if err := ensureRuntimeAbsent(filepath.Join("/opt/cni/bin", name)); err != nil {
			return err
		}
	}
	for name, data := range files {
		if err := writeRuntimeNew(filepath.Join("/opt/cni/bin", name), data, 0755); err != nil {
			return err
		}
	}
	return os.WriteFile(marker, []byte(c.K8s.CNIPlugins.Version+"\n"), 0600)
}

func clusterFingerprint(c clusterSettings) string {
	runtimeVersion, runc, adapter := c.Containerd.Version, c.Runc.Version, ""
	if c.Kubernetes.ContainerManager == "docker" {
		runtimeVersion, runc, adapter = c.Docker.Version, "", c.CRIDockerd.Version
	}
	raw, _ := json.Marshal(struct {
		Kubernetes                                     any
		Calico                                         string `json:",omitempty"`
		Endpoint                                       any
		Runtime, Runc, Adapter, CNI, CRI, Pod, Service string
	}{c.Kubernetes, c.Network.Calico.Version, c.ControlPlaneEndpoint, runtimeVersion, runc, adapter, c.CNIPlugins.Version, c.CRITools.Version, c.Network.PodSubnet, c.Network.ServiceSubnet})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func waitClusterNodesReady(ctx context.Context, expected []string, read func() ([]byte, error)) error {
	if len(expected) == 0 {
		return errors.New("缺少预期节点列表")
	}
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("等待全部节点 Ready: %w", err)
		}
		raw, err := read()
		if err != nil {
			return err
		}
		var list struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Status struct {
					Conditions []struct {
						Type   string `json:"type"`
						Status string `json:"status"`
					} `json:"conditions"`
				} `json:"status"`
			} `json:"items"`
		}
		if err = json.Unmarshal(raw, &list); err != nil {
			return err
		}
		ready := map[string]bool{}
		for _, node := range list.Items {
			for _, condition := range node.Status.Conditions {
				if condition.Type == "Ready" && condition.Status == "True" {
					ready[node.Metadata.Name] = true
				}
			}
		}
		all := true
		for _, name := range expected {
			if !ready[name] {
				all = false
				break
			}
		}
		if all {
			return nil
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("等待全部节点 Ready: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func probeClusterInstalled(ctx context.Context, c fileConfig) error {
	found := 0
	for _, tool := range []struct{ name, version string }{{"kubeadm", c.K8s.Kubernetes.Version}, {"kubelet", c.K8s.Kubernetes.Version}, {"kubectl", c.K8s.Kubernetes.Version}, {"crictl", c.K8s.CRITools.Version}} {
		path, err := exec.LookPath(tool.name)
		if err != nil {
			continue
		}
		found++
		if err = verifyKubeadmTool(ctx, path, tool.name, tool.version); err != nil {
			return err
		}
	}
	if found != 0 && found != 4 {
		return errors.New("节点存在部分 Kubernetes 工具；不覆盖半安装状态，请先检查原安装来源")
	}
	if found == 4 {
		if _, err := os.Stat("/etc/systemd/system/kubelet.service.d/10-kubeadm.conf"); err != nil {
			return errors.New("已有工具缺少受支持的 kubelet unit，不覆盖原安装")
		}
	}
	name, version := "containerd", c.K8s.Containerd.Version
	if c.K8s.Kubernetes.ContainerManager == "docker" {
		name, version = "dockerd", c.K8s.Docker.Version
	}
	if path, err := exec.LookPath(name); err == nil {
		if err = verifyRuntimeVersion(ctx, path, version); err != nil {
			return err
		}
	}
	if c.K8s.Kubernetes.ContainerManager == "containerd" {
		if path, err := exec.LookPath("runc"); err == nil {
			if err = verifyRuntimeVersion(ctx, path, c.K8s.Runc.Version); err != nil {
				return err
			}
		}
	} else if path, err := exec.LookPath("cri-dockerd"); err == nil {
		if err = verifyRuntimeVersion(ctx, path, c.K8s.CRIDockerd.Version); err != nil {
			return err
		}
	}
	return nil
}

func validateClusterServerVersion(raw []byte, expected string) error {
	var result struct {
		Server struct {
			Version string `json:"gitVersion"`
		} `json:"serverVersion"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.Server.Version != expected {
		return fmt.Errorf("集群服务端版本 %q 与目标 %s 不一致，不隐式升级或重新配置", result.Server.Version, expected)
	}
	return nil
}

func singleNodeTaintPatch(raw []byte) ([]byte, error) {
	var node struct {
		Metadata struct {
			Version string `json:"resourceVersion"`
		} `json:"metadata"`
		Spec struct {
			Taints []map[string]any `json:"taints"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil, err
	}
	kept := []map[string]any{}
	for _, taint := range node.Spec.Taints {
		key, _ := taint["key"].(string)
		if (key == "node-role.kubernetes.io/control-plane" || key == "node-role.kubernetes.io/master") && taint["effect"] == "NoSchedule" {
			continue
		}
		kept = append(kept, taint)
	}
	if len(kept) == len(node.Spec.Taints) {
		return nil, nil
	}
	if node.Metadata.Version == "" {
		return nil, errors.New("节点缺少 resourceVersion，拒绝覆盖并发 taint 更新")
	}
	return json.Marshal(map[string]any{"metadata": map[string]string{"resourceVersion": node.Metadata.Version}, "spec": map[string]any{"taints": kept}})
}
