package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Pin current-context once so subsequent reads and writes use the same cluster.
func selectedKubectl(ctx context.Context, kubeconfig, clusterContext string, out io.Writer) (func(...string) ([]byte, error), error) {
	base := []string{}
	if kubeconfig != "" {
		base = append(base, "--kubeconfig", kubeconfig)
	}
	if clusterContext == "" {
		raw, err := commandOutput(ctx, nil, "kubectl", append(append([]string{}, base...), "config", "current-context")...)
		if err != nil {
			return nil, err
		}
		clusterContext = strings.TrimSpace(string(raw))
		if clusterContext == "" {
			return nil, errors.New("未设置 current-context；请指定 --kubeconfig / --context")
		}
	}
	base = append(base, "--context", clusterContext, "--request-timeout=30s")
	fmt.Fprintln(out, "目标 Kubernetes context:", clusterContext)
	return func(args ...string) ([]byte, error) {
		return commandOutput(ctx, nil, "kubectl", append(append([]string{}, base...), args...)...)
	}, nil
}

func clusterHostProbe(c clusterSettings) string {
	probe := `set -eu; test "$(id -u)" = 0; test -d /run/systemd/system; uname -s; uname -m; command -v systemctl >/dev/null; if ! command -v apt-get >/dev/null && ! command -v dnf >/dev/null && ! command -v yum >/dev/null; then for x in tar modprobe sysctl swapoff conntrack socat iptables ip tc mount nsenter ethtool; do command -v "$x" >/dev/null || { echo "missing dependency and unsupported package manager: $x" >&2; exit 1; }; done; fi`
	state := filepath.ToSlash(filepath.Join("/var/lib/opskit/clusters", c.Name, "state.json"))
	return probe + `; if test -e /etc/kubernetes/kubelet.conf -o -e /etc/kubernetes/admin.conf -o -d /etc/kubernetes/manifests; then test -f ` + shellQuote(state) + ` || { echo 'node already belongs to an unmanaged cluster' >&2; exit 1; }; fi`
}

func newK8sCheckCommand(parent context.Context, out io.Writer) (*cobra.Command, error) {
	cmd := &cobra.Command{Use: "check", Long: `通过 SSH 只读检查部署目标，不下载、上传文件、安装软件或修改节点。
复用 create 的 SSH/sudo、Linux/systemd、架构和已有集群初步保护检查；报告磁盘及缺少的系统工具。
不验证节点间端口连通性、镜像可拉取性或完整部署归属，不保证后续部署成功。
缺少系统工具返回非零，create 可使用节点包管理器自动补齐。`}
	f := cmd.Flags()
	config := f.String("config", defaultConfigPath, "统一 YAML，读取 k8s 部署目标")
	timeout := f.Duration("timeout", 2*time.Minute, "全部节点检查的总超时")
	bindCommand(cmd, func() (int, error) {
		if f.NArg() != 0 || *timeout <= 0 {
			return 2, errors.New("多余参数或 timeout 无效")
		}
		c, err := commandConfig(f, *config)
		if err != nil {
			return 2, err
		}
		if err := validateCluster(&c.K8s); err != nil {
			return 2, err
		}
		ctx, cancel := context.WithTimeout(parent, *timeout)
		defer cancel()
		if err := checkClusterHosts(ctx, c.K8s, out, dialClusterSSH); err != nil {
			return taskError(ctx, err)
		}
		return 0, nil
	})
	return cmd, nil
}

func checkClusterHosts(ctx context.Context, c clusterSettings, out io.Writer, dial func(context.Context, clusterHost) (clusterRemote, error)) error {
	var failures []error
	for _, host := range c.Hosts {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		remote, err := dial(ctx, host)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: SSH 检查失败: %w", host.Name, err))
			continue
		}
		raw, probeErr := remote.Exec(ctx, true, clusterHostProbe(c))
		if probeErr == nil {
			fields := strings.Fields(string(raw))
			if len(fields) != 2 || fields[0] != "Linux" || (fields[1] != "x86_64" && fields[1] != "amd64" && fields[1] != "aarch64" && fields[1] != "arm64") {
				probeErr = errors.New("需要 Linux amd64/arm64 节点")
			}
		}
		if probeErr != nil {
			remote.Close()
			failures = append(failures, fmt.Errorf("%s: 预检失败: %w", host.Name, probeErr))
			continue
		}
		fmt.Fprintf(out, "[%s] SSH、sudo、Linux/systemd 和已有集群初步检查通过\n", host.Name)
		raw, err = remote.Exec(ctx, true, `set -eu; df -Pk / /var/lib; missing=0; for x in tar modprobe sysctl swapoff conntrack socat iptables ip tc mount nsenter ethtool; do if ! command -v "$x" >/dev/null; then echo "缺少系统工具: $x"; missing=1; fi; done; exit "$missing"`)
		remote.Close()
		fmt.Fprintf(out, "[%s] %s\n", host.Name, raw)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: 系统工具或磁盘检查失败: %w", host.Name, err))
		}
	}
	return errors.Join(failures...)
}

func newK8sStatusCommand(parent context.Context, out io.Writer) (*cobra.Command, error) {
	cmd := &cobra.Command{Use: "status", Long: `使用 kubectl 只读查看集群节点版本、Ready 和 kube-system Pod 状态。
支持 opskit 或手动 kubeadm 创建的集群；不连接 SSH、不修改集群。
默认读取 KUBECONFIG 或 ~/.kube/config；opskit 保存的独立 kubeconfig 需显式指定。
节点为空或任一节点未 Ready 返回非零；Pod 表用于诊断，不代替应用健康验收。`}
	f := cmd.Flags()
	config := f.String("config", defaultConfigPath, "统一 YAML（校验文件，不用 hosts 选择集群）")
	kubeconfig := f.String("kubeconfig", "", "kubeconfig 文件；默认 KUBECONFIG 或 ~/.kube/config")
	clusterContext := f.String("context", "", "目标 context；默认 current-context")
	timeout := f.Duration("timeout", time.Minute, "整个状态查询的总超时")
	bindCommand(cmd, func() (int, error) {
		if f.NArg() != 0 || *timeout <= 0 {
			return 2, errors.New("多余参数或 timeout 无效")
		}
		if _, err := commandConfig(f, *config); err != nil {
			return 2, err
		}
		ctx, cancel := context.WithTimeout(parent, *timeout)
		defer cancel()
		kube, err := selectedKubectl(ctx, *kubeconfig, *clusterContext, out)
		if err != nil {
			return 3, err
		}
		raw, err := kube("get", "nodes", "-o", "json")
		if err != nil {
			return taskError(ctx, err)
		}
		nodes, err := decodeNodes(raw)
		if err != nil {
			return 1, err
		}
		ready := len(nodes.Items) > 0
		fmt.Fprintln(out, "节点\tKubernetes\tReady")
		for _, node := range nodes.Items {
			status := "Unknown"
			for _, cond := range node.Status.Conditions {
				if cond.Type == "Ready" {
					status = cond.Status
				}
			}
			fmt.Fprintf(out, "%s\t%s\t%s\n", node.Metadata.Name, node.Status.NodeInfo.KubeletVersion, status)
			ready = ready && status == "True"
		}
		raw, err = kube("-n", "kube-system", "get", "pods", "-o", "wide")
		if err != nil {
			return taskError(ctx, err)
		}
		fmt.Fprint(out, string(raw))
		if !ready {
			return 1, errors.New("集群没有节点或存在未 Ready 节点")
		}
		return 0, nil
	})
	return cmd, nil
}
