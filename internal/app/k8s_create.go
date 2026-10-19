package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const clusterNodeProtocol = "opskit-k8s-node-v2"

type clusterNodePlan struct {
	Nodes   []string `json:"nodes"`
	Cluster string   `json:"cluster"`
	Host    string   `json:"host"`
	Address string   `json:"address"`
	Master  bool     `json:"master"`
	Single  bool     `json:"single"`
	Timeout string   `json:"timeout"`
	Calico  bool     `json:"calico"`
}
type clusterJoin struct {
	Token    string `json:"token"`
	CAHash   string `json:"caHash"`
	Endpoint string `json:"endpoint"`
}
type connectedClusterNode struct {
	host   clusterHost
	remote clusterRemote
	arch   string
	bundle string
	temp   string
}

func newK8sCreateCommand(parent context.Context, out, diag io.Writer) (*cobra.Command, error) {
	cmd := &cobra.Command{Use: "create"}
	f := cmd.Flags()

	path := f.String("config", defaultConfigPath, "统一 YAML；k8s 节点和版本配置")
	calicoVersion := f.String("calico-version", "", "Calico 版本；默认 k8s.network.calico.version，当前支持 v3.26.5")
	calicoBase := f.String("calico-download-base", "", "Calico 清单根地址；CALICO_DOWNLOAD_BASE / install.calico.download_base")
	version := f.String("version", "", "覆盖 k8s.kubernetes.version（kubeadm/kubelet/kubectl 同版）")
	dry := f.Bool("dry-run", false, "只校验和显示计划，不连接 SSH、不下载、不修改节点")
	timeout := f.Duration("timeout", 0, "覆盖 k8s.deploy.timeout，默认 45m")
	bindCommand(cmd, func() (int, error) {
		if f.NArg() != 0 {
			return 2, errors.New("k8s create 不接受位置参数；请在配置中设置 k8s.name: sxxpqp，使用 --config FILE 指定配置")
		}
		c, err := commandConfig(f, *path)
		if err != nil {
			return 2, err
		}
		if *version != "" {
			c.K8s.Kubernetes.Version = *version
		}
		setEnvFlag(f, "calico-download-base", "CALICO_DOWNLOAD_BASE")
		setConfigFlag(f, "calico-download-base", c.Install.Calico.DownloadBase)
		if *calicoBase != "" {
			c.Install.Calico.DownloadBase = *calicoBase
		}
		if *calicoVersion != "" {
			c.K8s.Network.Calico.Version = *calicoVersion
		}
		if err = mergeClusterDefaults(&c); err != nil {
			return 2, err
		}
		if err = validateCluster(&c.K8s); err != nil {
			return 2, err
		}
		if c.K8s.Network.Manifest == "" {
			if _, err = clusterCalicoSource(c); err != nil {
				return 2, err
			}
		}

		duration := 45 * time.Minute
		if c.K8s.Deploy.Timeout != "" {
			duration, err = time.ParseDuration(c.K8s.Deploy.Timeout)
			if err != nil {
				return 2, err
			}
		}
		if explicitFlag(f, "timeout") {
			duration = *timeout
		}
		if duration <= 0 {
			return 2, errors.New("timeout 必须大于零")
		}
		c.K8s.Deploy.Timeout = duration.String()
		if c.K8s.Network.Manifest != "" {
			if _, err = readClusterNetwork(c.K8s.Network.Manifest); err != nil {
				return 2, err
			}
			fmt.Fprintln(out, "CNI 本地清单:", c.K8s.Network.Manifest)
		} else {
			source, _ := clusterCalicoSource(c)
			fmt.Fprintf(out, "Calico %s；Pod CIDR=%s；下载并校验: %s\n", c.K8s.Network.Calico.Version, c.K8s.Network.PodSubnet, source)
		}

		for _, host := range c.K8s.Hosts {
			fmt.Fprintf(out, "节点 %s: SSH %s@%s:%d；集群地址 %s\n", host.Name, host.User, host.Address, host.Port, host.InternalAddress)
		}
		fmt.Fprintf(out, "Kubernetes %s（kubeadm/kubelet/kubectl 同版），运行时 %s；并发 %d，总超时 %s；KubeSphere=%t\n", c.K8s.Kubernetes.Version, c.K8s.Kubernetes.ContainerManager, c.K8s.Deploy.Concurrency, duration, c.K8s.KubeSphere.Enabled)
		if *dry {
			fmt.Fprintln(out, "预览完成；未连接节点或读取认证秘密。")
			return 0, nil
		}
		output := c.K8s.Deploy.OutputDir
		if output == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return 1, err
			}
			output = filepath.Join(home, ".opskit", "clusters", c.K8s.Name)
		}
		if err = os.MkdirAll(output, 0700); err != nil {
			return 1, err
		}
		lock, err := os.OpenFile(filepath.Join(output, "deploy.lock"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return 1, fmt.Errorf("部署锁已存在或不可写；确认没有部署运行后再处理: %w", err)
		}
		lock.Close()
		defer os.Remove(filepath.Join(output, "deploy.lock"))
		log, err := os.OpenFile(filepath.Join(output, "deploy.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return 1, err
		}
		defer log.Close()
		writer := &clusterLockedWriter{w: io.MultiWriter(out, log)}
		ctx, cancel := context.WithTimeout(parent, duration)
		defer cancel()
		if err = executeK8sCluster(ctx, c, output, writer, dialClusterSSH); err != nil {
			return 1, err
		}
		return 0, nil
	})
	return cmd, nil
}

type clusterLockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *clusterLockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}
func clusterParallel(ctx context.Context, nodes []*connectedClusterNode, limit int, fn func(context.Context, *connectedClusterNode) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, limit)
	errs := make(chan error, len(nodes))
	var wg sync.WaitGroup
	for _, node := range nodes {
		wg.Add(1)
		go func(node *connectedClusterNode) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
			defer func() { <-sem }()
			if err := fn(ctx, node); err != nil {
				errs <- fmt.Errorf("%s: %w", node.host.Name, err)
				cancel()
			}
		}(node)
	}
	wg.Wait()
	close(errs)
	var failures []error
	for err := range errs {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
func executeK8sCluster(ctx context.Context, c fileConfig, output string, out io.Writer, dial func(context.Context, clusterHost, string) (clusterRemote, error)) error {
	autoCalico := c.K8s.Network.Manifest == ""
	var nodes []*connectedClusterNode
	defer func() {
		for _, node := range nodes {
			node.remote.Close()
		}
	}()
	// Complete read-only SSH/root/OS checks on every host before transferring or modifying files.
	for _, h := range c.K8s.Hosts {
		remote, err := dial(ctx, h, c.K8s.Deploy.KnownHostsFile)
		if err != nil {
			return fmt.Errorf("%s: %w", h.Name, err)
		}
		node := &connectedClusterNode{host: h, remote: remote}
		nodes = append(nodes, node)
		probe := `set -eu; test "$(id -u)" = 0; test -d /run/systemd/system; uname -s; uname -m; for x in tar systemctl modprobe sysctl swapoff conntrack socat iptables ip tc mount nsenter ethtool; do command -v "$x" >/dev/null || { echo "missing dependency: $x" >&2; exit 1; }; done`
		state := filepath.ToSlash(filepath.Join("/var/lib/opskit/clusters", c.K8s.Name, "state.json"))
		probe += `; if test -e /etc/kubernetes/kubelet.conf -o -e /etc/kubernetes/admin.conf -o -d /etc/kubernetes/manifests; then test -f ` + shellQuote(state) + ` || { echo 'node already belongs to an unmanaged cluster' >&2; exit 1; }; fi`
		raw, err := remote.Exec(ctx, true, probe)
		if err != nil {
			return fmt.Errorf("%s: 全节点预检失败: %w", h.Name, err)
		}
		fields := strings.Fields(string(raw))
		if len(fields) != 2 || fields[0] != "Linux" {
			return fmt.Errorf("%s: 只支持 Linux 节点", h.Name)
		}
		switch fields[1] {
		case "x86_64", "amd64":
			node.arch = "amd64"
		case "aarch64", "arm64":
			node.arch = "arm64"
		default:
			return fmt.Errorf("%s: 不支持架构 %s", h.Name, fields[1])
		}
		fmt.Fprintf(out, "[%s] SSH、主机身份、提权和系统依赖检查通过（%s）\n", h.Name, node.arch)
	}
	work, err := os.MkdirTemp("", "opskit-cluster-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err = prepareClusterNetwork(ctx, &c, work, out); err != nil {
		return err
	}
	binaries := map[string]string{}
	caches := map[string]string{}
	for _, node := range nodes {
		if _, ok := binaries[node.arch]; ok {
			continue
		}
		binary, err := clusterNodeBinary(c.K8s, node.arch)
		if err != nil {
			return err
		}
		binaries[node.arch] = binary
		root := filepath.Join(work, node.arch)
		if err = os.Mkdir(root, 0700); err != nil {
			return err
		}
		caches[node.arch] = root
		if err = fetchClusterAssets(ctx, clusterAssets(c, node.arch), root, out); err != nil {
			return err
		}
	}
	random := make([]byte, 12)
	if _, err = rand.Read(random); err != nil {
		return err
	}
	id := hex.EncodeToString(random)
	for _, node := range nodes {
		node.temp = "/tmp/opskit-" + id
		node.bundle = "/var/lib/opskit/clusters/" + c.K8s.Name + "/run-" + id
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, n := range nodes {
			_, _ = n.remote.Exec(cleanup, true, "rm -rf -- "+shellQuote(n.bundle))
			_, _ = n.remote.Exec(cleanup, false, "rm -rf -- "+shellQuote(n.temp))
		}
	}()
	if err = clusterParallel(ctx, nodes, c.K8s.Deploy.Concurrency, func(ctx context.Context, n *connectedClusterNode) error {
		local := filepath.Join(work, n.host.Name)
		if err := os.Mkdir(local, 0700); err != nil {
			return err
		}
		cfg := fileConfig{SchemaVersion: 1, Install: c.Install, K8s: c.K8s, Mirrors: c.Mirrors}
		cfg.K8s.Hosts = nil
		cfg.K8s.Deploy.NodeBinaries = nil
		cfg.K8s.Deploy.KnownHostsFile = ""
		cfg.K8s.Network.Manifest = ""
		if !autoCalico {
			cfg.K8s.Network.Calico.Version = ""
		}
		cfg.Install.DownloadDir = ""
		cfg.Install.Docker.Method = "binary"
		raw, err := yaml.Marshal(cfg)
		if err != nil {
			return err
		}
		configPath := filepath.Join(local, "node.yaml")
		if err = bytesFile(configPath, raw); err != nil {
			return err
		}
		var expected []string
		for _, h := range c.K8s.Hosts {
			expected = append(expected, h.Name)
		}
		plan := clusterNodePlan{Nodes: expected, Cluster: c.K8s.Name, Host: n.host.Name, Address: n.host.InternalAddress, Master: n.host.Name == c.K8s.RoleGroups.Master[0], Single: len(c.K8s.RoleGroups.Worker) == 0, Calico: autoCalico, Timeout: c.K8s.Deploy.Timeout}
		raw, err = json.Marshal(plan)
		if err != nil {
			return err
		}
		planPath := filepath.Join(local, "plan.json")
		if err = bytesFile(planPath, raw); err != nil {
			return err
		}
		files := map[string]string{"opskit": binaries[n.arch], "node.yaml": configPath, "plan.json": planPath, "network.yaml": c.K8s.Network.Manifest}
		entries, err := os.ReadDir(caches[n.arch])
		if err != nil {
			return err
		}
		for _, entry := range entries {
			files[entry.Name()] = filepath.Join(caches[n.arch], entry.Name())
		}
		archive := filepath.Join(local, "bundle.tar")
		f, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		err = writeClusterArchive(f, files)
		ce := f.Close()
		if err != nil {
			return err
		}
		if ce != nil {
			return ce
		}
		if _, err = n.remote.Exec(ctx, false, "umask 077; mkdir -- "+shellQuote(n.temp)); err != nil {
			return err
		}
		f, err = os.Open(archive)
		if err != nil {
			return err
		}
		err = n.remote.Upload(ctx, n.temp+"/bundle.tar", f)
		f.Close()
		if err != nil {
			return err
		}
		if _, err = n.remote.Exec(ctx, true, "umask 077; mkdir -p -- "+shellQuote(n.bundle)+"; tar -xf "+shellQuote(n.temp+"/bundle.tar")+" -C "+shellQuote(n.bundle)); err != nil {
			return err
		}
		protocol, err := n.remote.Exec(ctx, true, shellQuote(n.bundle+"/opskit")+" k8s node --protocol")
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(protocol)) != clusterNodeProtocol {
			return errors.New("节点 opskit 构建不支持当前编排协议，请提供同一份代码的 Linux 构建")
		}
		return nil
	}); err != nil {
		return err
	}
	// Probe every node's persistent ownership before setup. A resume never adopts a foreign cluster.
	if err = clusterParallel(ctx, nodes, c.K8s.Deploy.Concurrency, func(ctx context.Context, n *connectedClusterNode) error {
		_, err := clusterPhase(ctx, n, "probe", out)
		return err
	}); err != nil {
		return err
	}
	if err = clusterParallel(ctx, nodes, c.K8s.Deploy.Concurrency, func(ctx context.Context, n *connectedClusterNode) error {
		_, err := clusterPhase(ctx, n, "setup", out)
		return err
	}); err != nil {
		return err
	}
	var master *connectedClusterNode
	var workers []*connectedClusterNode
	for _, n := range nodes {
		if n.host.Name == c.K8s.RoleGroups.Master[0] {
			master = n
		} else {
			workers = append(workers, n)
		}
	}
	if _, err = clusterPhase(ctx, master, "init", out); err != nil {
		return err
	}
	// Save access under this deployment output directory, never under ~/.kube/config.
	raw, err := clusterPhase(ctx, master, "kubeconfig", io.Discard)
	if err != nil {
		return err
	}
	raw, err = rewriteClusterKubeconfig(raw, net.JoinHostPort(c.K8s.ControlPlaneEndpoint.Address, fmt.Sprint(c.K8s.ControlPlaneEndpoint.Port)))
	if err != nil {
		return err
	}
	access := filepath.Join(output, "kubeconfig")
	if err = writeConfigFile(access, raw, out); err != nil {
		return err
	}
	if err = os.Chmod(access, 0600); err != nil {
		return err
	}
	if len(workers) > 0 {
		credentials, err := clusterPhase(ctx, master, "credentials", io.Discard)
		if err != nil {
			return err
		}
		var join clusterJoin
		if err = json.Unmarshal(credentials, &join); err != nil {
			return err
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, _ = master.remote.Exec(cleanup, true, "kubeadm token delete "+shellQuote(strings.Split(join.Token, ".")[0])+" --kubeconfig /etc/kubernetes/admin.conf")
		}()
		if err = clusterParallel(ctx, workers, c.K8s.Deploy.Concurrency, func(ctx context.Context, n *connectedClusterNode) error {
			if err := n.remote.Upload(ctx, n.temp+"/join.json", bytes.NewReader(credentials)); err != nil {
				return err
			}
			if _, err := n.remote.Exec(ctx, true, "install -m 600 "+shellQuote(n.temp+"/join.json")+" "+shellQuote(n.bundle+"/join.json")); err != nil {
				return err
			}
			_, err := clusterPhase(ctx, n, "join", out)
			return err
		}); err != nil {
			return err
		}
		if _, err = master.remote.Exec(ctx, true, "kubeadm token delete "+shellQuote(strings.Split(join.Token, ".")[0])+" --kubeconfig /etc/kubernetes/admin.conf"); err != nil {
			return fmt.Errorf("撤销本次 join token 失败: %w", err)
		}
	}
	if _, err = clusterPhase(ctx, master, "ready", out); err != nil {
		return err
	}
	if c.K8s.KubeSphere.Enabled {
		if _, err = clusterPhase(ctx, master, "sphere", out); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "集群 %s 部署完成，节点已通过 Ready 检查。kubeconfig: %s\n", c.K8s.Name, access)
	return nil
}
func clusterPhase(ctx context.Context, n *connectedClusterNode, phase string, out io.Writer) ([]byte, error) {
	fmt.Fprintf(out, "[%s/%s] 开始\n", n.host.Name, phase)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command := shellQuote(n.bundle+"/opskit") + " k8s node --bundle " + shellQuote(n.bundle) + " --phase " + shellQuote(phase)
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, context.DeadlineExceeded
		}
		command += " --timeout " + shellQuote(remaining.String())
	}
	raw, err := n.remote.Exec(ctx, true, command)
	if len(raw) > 0 {
		fmt.Fprintf(out, "[%s/%s] %s\n", n.host.Name, phase, redactClusterLog(string(raw), n.host))
	}
	return raw, err
}
func redactClusterLog(text string, h clusterHost) string {
	for _, value := range []string{h.Password, h.SudoPassword, os.Getenv(h.PasswordEnv), os.Getenv(h.SudoPasswordEnv)} {
		if value != "" {
			text = strings.ReplaceAll(text, value, "<redacted>")
		}
	}
	return regexp.MustCompile(`\b[a-z0-9]{6}\.[a-z0-9]{16}\b`).ReplaceAllString(text, "<join-token>")
}
func rewriteClusterKubeconfig(raw []byte, endpoint string) ([]byte, error) {
	var data map[string]any
	if err := yaml.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	clusters, ok := data["clusters"].([]any)
	if !ok || len(clusters) != 1 {
		return nil, errors.New("管理 kubeconfig 的集群列表无效")
	}
	entry, ok := clusters[0].(map[string]any)
	if !ok {
		return nil, errors.New("kubeconfig cluster 无效")
	}
	cluster, ok := entry["cluster"].(map[string]any)
	if !ok {
		return nil, errors.New("kubeconfig cluster 无效")
	}
	cluster["server"] = "https://" + endpoint
	return yaml.Marshal(data)
}
