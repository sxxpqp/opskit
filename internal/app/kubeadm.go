package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	templates "github.com/sxxpqp/opskit"
	"gopkg.in/yaml.v3"
)

type kubeadmSettings struct {
	ClusterName          string `yaml:"cluster_name"`
	NodeIP               string `yaml:"node_ip"`
	AdvertiseAddress     string `yaml:"advertise_address"`
	ControlPlaneEndpoint string `yaml:"control_plane_endpoint"`
	NodeName             string `yaml:"node_name"`
	PodSubnet            string `yaml:"pod_subnet"`
	ServiceSubnet        string `yaml:"service_subnet"`
	CRISocket            string `yaml:"cri_socket"`
	ImageRepository      string `yaml:"image_repository"`
	NetworkManifest      string `yaml:"network_manifest"`
	JoinEndpoint         string `yaml:"join_endpoint"`
	Token                string `yaml:"token"`
	CAHash               string `yaml:"discovery_token_ca_cert_hash"`
}

func newKubeadmCommand(parent context.Context, action string, out, diag io.Writer) (*cobra.Command, error) {
	var defaults fileConfig
	if err := yamlClusterDefaults(&defaults); err != nil {
		return nil, err
	}
	cmd := &cobra.Command{Use: action}
	f := cmd.Flags()
	config := f.String("config", defaultConfigPath, "统一配置文件")
	version := f.String("version", "", "Kubernetes 完整版本 v1.X.Y；支持已配置的 1.24～1.37")
	dry := f.Bool("dry-run", false, "仅预览，不下载、不调用外部工具、不修改主机或集群")
	timeout := f.Duration("timeout", 30*time.Minute, "本次命令总超时")
	var base, kubeletBase, kubectlBase, criBase, criVersion, downloadDir *string
	o := defaults.Kubeadm
	if action == "install" {
		base = f.String("kubeadm-download-base", defaults.Install.Kubeadm.DownloadBase, "kubeadm 根地址；KUBEADM_DOWNLOAD_BASE / install.kubeadm.download_base")
		kubeletBase = f.String("kubelet-download-base", defaults.Install.Kubelet.DownloadBase, "kubelet 根地址；KUBELET_DOWNLOAD_BASE / install.kubelet.download_base")
		kubectlBase = f.String("kubectl-download-base", defaults.Install.Kubectl.DownloadBase, "kubectl 根地址；KUBECTL_DOWNLOAD_BASE / install.kubectl.download_base")
		criBase = f.String("cri-tools-download-base", defaults.Install.CRITools.DownloadBase, "crictl GitHub release 根地址；CRI_TOOLS_DOWNLOAD_BASE / install.cri_tools.download_base")
		criVersion = f.String("cri-tools-version", defaults.Versions.CRITools, "crictl 完整版本，与 Kubernetes 同 minor；k8s.criTools.version")
		downloadDir = f.String("download-dir", "", "统一下载目录，默认 ~/.opskit/downloads")
	}
	if action == "init" || action == "join" {
		f.StringVar(&o.CRISocket, "cri-socket", o.CRISocket, "containerd Unix socket；kubeadm.cri_socket")
		f.StringVar(&o.NodeName, "node-name", "", "节点名称；默认主机名")
		f.StringVar(&o.NodeIP, "node-ip", "", "kubelet 使用的本机 IPv4；多网卡时指定")
	}
	if action == "init" {
		f.StringVar(&o.AdvertiseAddress, "advertise-address", "", "API Server 本机 IPv4，必填")
		f.StringVar(&o.ControlPlaneEndpoint, "control-plane-endpoint", "", "控制平面 DNS/IP:端口")
		f.StringVar(&o.PodSubnet, "pod-subnet", o.PodSubnet, "Pod IPv4 CIDR，须匹配网络插件")
		f.StringVar(&o.ServiceSubnet, "service-subnet", o.ServiceSubnet, "Service IPv4 CIDR")
		f.StringVar(&o.ImageRepository, "image-repository", o.ImageRepository, "kubeadm 系统镜像根地址；kubeadm.image_repository")
		f.StringVar(&o.NetworkManifest, "network-manifest", "", "可选，本地固定版本 CNI 网络清单；不下载远端 URL")
	}
	if action == "join" {
		f.StringVar(&o.JoinEndpoint, "endpoint", "", "控制平面 DNS/IP:端口")
		f.StringVar(&o.Token, "token", "", "加入 token；优先使用 YAML 避免进入 shell 历史")
		f.StringVar(&o.CAHash, "discovery-token-ca-cert-hash", "", "控制平面 CA 公钥 sha256 摘要，必填")
	}
	bindCommand(cmd, func() (int, error) {
		if f.NArg() != 0 || *timeout <= 0 {
			return 2, errors.New("参数或 timeout 无效")
		}
		c, err := commandConfig(f, *config)
		if err != nil {
			return 2, err
		}
		setConfigFlag(f, "version", c.Versions.Kubernetes)
		minor, err := kubeadmMinor(*version)
		if err != nil {
			return 2, err
		}
		// Version flags take precedence; auto-pinned dependencies must follow
		// the effective target version, not the YAML's original version.
		c.K8s.Kubernetes.Version = *version
		if err = resolveClusterComponentVersions(&c); err != nil {
			return 2, err
		}
		ctx, cancel := context.WithTimeout(parent, *timeout)
		defer cancel()
		if action == "install" {
			for _, item := range []struct{ flag, env, value string }{
				{"kubeadm-download-base", "KUBEADM_DOWNLOAD_BASE", c.Install.Kubeadm.DownloadBase},
				{"kubelet-download-base", "KUBELET_DOWNLOAD_BASE", c.Install.Kubelet.DownloadBase},
				{"kubectl-download-base", "KUBECTL_DOWNLOAD_BASE", c.Install.Kubectl.DownloadBase},
				{"cri-tools-download-base", "CRI_TOOLS_DOWNLOAD_BASE", c.Install.CRITools.DownloadBase},
			} {
				setEnvFlag(f, item.flag, item.env)
				setConfigFlag(f, item.flag, item.value)
			}
			setConfigFlag(f, "cri-tools-version", c.Versions.CRITools)
			setConfigFlag(f, "download-dir", c.Install.DownloadDir)
			criMinor, e := kubeadmMinor(*criVersion)
			if e != nil || criMinor != minor {
				return 2, errors.New("cri-tools-version 必须与 Kubernetes 同 minor，使用 k8s.criTools.version 或 --cri-tools-version 指定")
			}
			bases := map[string]string{"kubeadm": *base, "kubelet": *kubeletBase, "kubectl": *kubectlBase}
			for _, value := range append([]string{*base, *kubeletBase, *kubectlBase}, *criBase) {
				if e := validateKubeadmBase(value); e != nil {
					return 2, e
				}
			}
			if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
				return 2, errors.New("仅支持 Linux amd64/arm64")
			}
			fmt.Fprintf(out, "kubeadm install: Kubernetes %s，crictl %s，linux/%s\n", *version, *criVersion, runtime.GOARCH)
			for _, name := range []string{"kubeadm", "kubelet", "kubectl"} {
				fmt.Fprintf(out, "%s: %s/%s/bin/linux/%s/%s (+ .sha256)\n", name, strings.TrimRight(bases[name], "/"), *version, runtime.GOARCH, name)
			}
			criURL := fmt.Sprintf("%s/kubernetes-sigs/cri-tools/releases/download/%s/crictl-%s-linux-%s.tar.gz", strings.TrimRight(*criBase, "/"), *criVersion, *criVersion, runtime.GOARCH)
			fmt.Fprintf(out, "crictl: %s (+ .sha256)\n目标: /usr/local/bin/{kubeadm,kubelet,kubectl,crictl} 和 kubelet systemd unit；不覆盖已有安装。\n", criURL)
			if *dry {
				return 0, nil
			}
			if err := kubeadmHost(); err != nil {
				return 1, err
			}
			if *downloadDir == "" {
				*downloadDir, err = defaultDownloadDir()
				if err != nil {
					return 1, err
				}
			}
			if err := installKubeadmTools(ctx, *version, *criVersion, bases, criURL, *downloadDir, out, diag); err != nil {
				return 1, err
			}
			return 0, nil
		}
		for _, pair := range []struct{ name, value string }{
			{"cri-socket", c.Kubeadm.CRISocket}, {"node-name", c.Kubeadm.NodeName}, {"advertise-address", c.Kubeadm.AdvertiseAddress},
			{"control-plane-endpoint", c.Kubeadm.ControlPlaneEndpoint}, {"pod-subnet", c.Kubeadm.PodSubnet}, {"service-subnet", c.Kubeadm.ServiceSubnet},
			{"image-repository", c.Kubeadm.ImageRepository}, {"network-manifest", c.Kubeadm.NetworkManifest}, {"endpoint", c.Kubeadm.JoinEndpoint},
			{"token", c.Kubeadm.Token}, {"discovery-token-ca-cert-hash", c.Kubeadm.CAHash},
		} {
			if f.Lookup(pair.name) != nil {
				setConfigFlag(f, pair.name, pair.value)
			}
		}
		if action == "prepare" {
			fmt.Fprintf(out, "kubeadm prepare: Kubernetes %s；加载 overlay/br_netfilter，持久化转发 sysctl，禁用 swap 并备份 fstab。\n", *version)
			if *dry {
				return 0, nil
			}
			if err := kubeadmHost(); err != nil {
				return 1, err
			}
			if err := kubeadmFreshNode(); err != nil {
				return 1, err
			}
			if err := prepareKubeadmHost(ctx, out, diag); err != nil {
				return 1, err
			}
			return 0, nil
		}
		data, err := renderKubeadm(action, *version, o)
		if err != nil {
			return 2, err
		}
		var manifest []byte
		if action == "init" && o.NetworkManifest != "" {
			manifest, err = os.ReadFile(o.NetworkManifest)
			if err != nil {
				return 2, fmt.Errorf("读取本地网络清单: %w", err)
			}
			if len(bytes.TrimSpace(manifest)) == 0 {
				return 2, errors.New("网络清单为空")
			}
		}
		fmt.Fprintf(out, "kubeadm %s: Kubernetes %s；containerd=%s；node=%s\n", action, *version, o.CRISocket, o.NodeName)
		if *dry {
			safe := data
			if action == "join" {
				safe = bytes.ReplaceAll(data, []byte(o.Token), []byte("<redacted>"))
			}
			_, err = out.Write(safe)
			if action == "init" {
				fmt.Fprintf(out, "网络清单: %s（留空时需另行安装，节点在此之前可能 NotReady）\n", o.NetworkManifest)
			}
			return 0, err
		}
		if err := kubeadmHost(); err != nil {
			return 1, err
		}
		if err := kubeadmFreshNode(); err != nil {
			return 1, err
		}
		if action == "init" {
			if err := checkKubeadmAdvertiseAddress(o.AdvertiseAddress); err != nil {
				return 1, err
			}
		}
		if err := checkKubeadmNode(ctx, *version, minor, o.CRISocket); err != nil {
			return 1, err
		}
		// Keep bootstrap credentials in a private temporary config, never in command arguments or error output.
		tmp, err := os.CreateTemp("", "opskit-kubeadm-*.yaml")
		if err != nil {
			return 1, err
		}
		defer os.Remove(tmp.Name())
		if _, err = tmp.Write(data); err != nil {
			tmp.Close()
			return 1, err
		}
		if err = tmp.Close(); err != nil {
			return 1, err
		}
		if err = commandRun(ctx, nil, out, diag, "systemctl", "enable", "--now", "kubelet"); err != nil {
			return 1, err
		}
		if err = commandRun(ctx, nil, out, diag, "kubeadm", action, "--config", tmp.Name()); err != nil {
			return 1, err
		}
		if action == "init" {
			if len(manifest) > 0 {
				if err = commandRun(ctx, manifest, out, diag, "kubectl", "--kubeconfig", "/etc/kubernetes/admin.conf", "apply", "-f", "-"); err != nil {
					return 1, err
				}
			}
			fmt.Fprintln(out, "控制平面初始化完成；使用 export KUBECONFIG=/etc/kubernetes/admin.conf。网络和存储就绪需单独验收；未覆盖 ~/.kube/config。")
		} else {
			fmt.Fprintln(out, "工作节点加入完成；请在控制平面检查 kubectl get nodes。")
		}
		return 0, nil
	})
	return cmd, nil
}

func kubeadmMinor(version string) (int, error) {
	if !versionPattern.MatchString(version) {
		return 0, errors.New("必须指定完整 --version vX.Y.Z 或 k8s.kubernetes.version；不使用 latest")
	}
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	minor, _ := strconv.Atoi(parts[1])
	if parts[0] != "1" || minor < 24 || minor > 37 {
		return 0, errors.New("当前 kubeadm 路线仅支持 Kubernetes 1.24～1.37（v1beta3/v1beta4）；新增 minor 需先添加依赖兼容配置及测试")
	}
	return minor, nil
}
func validateKubeadmBase(base string) error {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("下载根地址必须为无凭据、查询参数或片段的 HTTP(S) URL")
	}
	return nil
}
func kubeadmHost() error {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return errors.New("实际执行需要 Linux amd64/arm64；其他系统仅支持 --dry-run")
	}
	if os.Geteuid() != 0 {
		return errors.New("需要 root 权限")
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return errors.New("需要正在运行的 systemd")
	}
	return nil
}
func kubeadmFreshNode() error {
	for _, path := range []string{"/etc/kubernetes/kubelet.conf", "/etc/kubernetes/admin.conf", "/etc/kubernetes/manifests", "/var/lib/etcd/member"} {
		if err := ensureRuntimeAbsent(path); err != nil {
			return fmt.Errorf("节点已有集群数据，不初始化、不加入、不自动 reset: %w", err)
		}
	}
	return nil
}

// kubeadm switched to v1beta4 in Kubernetes 1.31. kubeletExtraArgs also
// changed from a map to a list of {name, value} pairs in that API.
func kubeadmConfigAPIVersion(minor int) string {
	if minor >= 31 {
		return "kubeadm.k8s.io/v1beta4"
	}
	return "kubeadm.k8s.io/v1beta3"
}

func kubeadmKubeletExtraArgs(minor int, nodeIP string) any {
	if minor >= 31 {
		return []map[string]string{{"name": "node-ip", "value": nodeIP}}
	}
	return map[string]string{"node-ip": nodeIP}
}

// Use structured YAML rather than passing secrets in subprocess arguments.
func renderKubeadm(action, version string, o kubeadmSettings) ([]byte, error) {
	minor, err := kubeadmMinor(version)
	if err != nil {
		return nil, err
	}
	apiVersion := kubeadmConfigAPIVersion(minor)
	if o.NodeName != "" && !regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`).MatchString(o.NodeName) {
		return nil, errors.New("node-name 必须是小写 DNS 名称")
	}
	// CRI endpoints belong to Linux nodes even when the management host is Windows.
	u, err := url.Parse(o.CRISocket)
	if err != nil || u.Scheme != "unix" || u.Host != "" || !path.IsAbs(u.Path) || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("cri-socket 必须为本地 Unix socket URL")
	}
	registration := map[string]any{"criSocket": o.CRISocket}
	if o.NodeIP != "" {
		ip := net.ParseIP(o.NodeIP)
		if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsLoopback() {
			return nil, errors.New("node-ip 必须为本机可达 IPv4")
		}
		registration["kubeletExtraArgs"] = kubeadmKubeletExtraArgs(minor, o.NodeIP)
	} else if action == "init" {
		registration["kubeletExtraArgs"] = kubeadmKubeletExtraArgs(minor, o.AdvertiseAddress)
	}
	if o.NodeName != "" {
		registration["name"] = o.NodeName
	}
	if action == "join" {
		if err := validateKubeadmEndpoint(o.JoinEndpoint); err != nil {
			return nil, err
		}
		if !regexp.MustCompile(`^[a-z0-9]{6}\.[a-z0-9]{16}$`).MatchString(o.Token) {
			return nil, errors.New("join token 格式必须为 6位字符.16位字符")
		}
		if !regexp.MustCompile(`^sha256:[0-9a-fA-F]{64}$`).MatchString(o.CAHash) {
			return nil, errors.New("必须提供 discovery-token-ca-cert-hash sha256:<64位摘要>；不跳过 CA 校验")
		}
		return yaml.Marshal(map[string]any{"apiVersion": apiVersion, "kind": "JoinConfiguration", "nodeRegistration": registration, "discovery": map[string]any{"bootstrapToken": map[string]any{"apiServerEndpoint": o.JoinEndpoint, "token": o.Token, "caCertHashes": []string{o.CAHash}}}})
	}
	if action != "init" {
		return nil, errors.New("仅支持 init 或 join 配置")
	}
	ip := net.ParseIP(o.AdvertiseAddress)
	if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
		return nil, errors.New("advertise-address 必须为可被其他节点访问的本机 IPv4")
	}
	podIP, pod, err := net.ParseCIDR(o.PodSubnet)
	if err != nil || podIP.To4() == nil {
		return nil, errors.New("pod-subnet 必须为 IPv4 CIDR")
	}
	svcIP, svc, err := net.ParseCIDR(o.ServiceSubnet)
	if err != nil || svcIP.To4() == nil {
		return nil, errors.New("service-subnet 必须为 IPv4 CIDR")
	}
	if pod.Contains(svcIP) || svc.Contains(podIP) || pod.Contains(ip) || svc.Contains(ip) {
		return nil, errors.New("Pod/Service 网段必须互不重叠，且不能包含 advertise-address")
	}
	if o.ControlPlaneEndpoint != "" {
		if err := validateKubeadmEndpoint(o.ControlPlaneEndpoint); err != nil {
			return nil, err
		}
	}
	if o.ImageRepository == "" || strings.ContainsAny(o.ImageRepository, " \t\r\n@") || strings.Contains(o.ImageRepository, "://") {
		return nil, errors.New("image-repository 必须为非空镜像仓库路径，不带 URL 协议")
	}
	documents := []map[string]any{
		{"apiVersion": apiVersion, "kind": "InitConfiguration", "nodeRegistration": registration, "localAPIEndpoint": map[string]any{"advertiseAddress": o.AdvertiseAddress, "bindPort": 6443}},
		{"apiVersion": apiVersion, "kind": "ClusterConfiguration", "kubernetesVersion": version, "imageRepository": o.ImageRepository, "networking": map[string]any{"podSubnet": o.PodSubnet, "serviceSubnet": o.ServiceSubnet}},
		{"apiVersion": "kubelet.config.k8s.io/v1beta1", "kind": "KubeletConfiguration", "cgroupDriver": "systemd"},
	}
	if o.ClusterName != "" {
		documents[1]["clusterName"] = o.ClusterName
	}
	if o.ControlPlaneEndpoint != "" {
		documents[1]["controlPlaneEndpoint"] = o.ControlPlaneEndpoint
	}
	var result bytes.Buffer
	enc := yaml.NewEncoder(&result)
	for _, doc := range documents {
		if err := enc.Encode(doc); err != nil {
			return nil, err
		}
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}
func validateKubeadmEndpoint(endpoint string) error {
	host, port, err := net.SplitHostPort(endpoint)
	n, e := strconv.Atoi(port)
	if err != nil || e != nil || host == "" || n < 1 || n > 65535 || strings.ContainsAny(host, " /\\\t\r\n") {
		return errors.New("控制平面 endpoint 必须为 DNS/IP:端口")
	}
	return nil
}
