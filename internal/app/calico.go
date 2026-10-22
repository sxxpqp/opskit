package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newCalicoInstallCommand(parent context.Context, out, diag io.Writer) (*cobra.Command, error) {
	var defaults fileConfig
	if err := yamlClusterDefaults(&defaults); err != nil {
		return nil, err
	}
	cmd := &cobra.Command{Use: "install", Long: `在已由 kubeadm 初始化的集群单独安装 Calico，不执行 kubeadm init/join。
使用 kubectl、KUBECONFIG 或 ~/.kube/config；--kubeconfig / --context 可指定目标。
检查实际服务端版本、kubeadm Pod CIDR 和已有网络插件；拒绝替换已有 Calico 或已识别的其他 CNI。
当前固定支持 Calico v3.26.5 / Kubernetes 1.24～1.28；默认等待网络工作负载就绪。
--dry-run 只校验本地配置并展示计划，不连接集群、不下载。`}
	f := cmd.Flags()
	config := f.String("config", defaultConfigPath, "统一 YAML 配置")
	version := f.String("version", "", "Calico 版本；默认 k8s.network.calico.version")
	podCIDR := f.String("pod-subnet", "", "Pod IPv4 CIDR；默认 k8s.network.kubePodsCIDR，必须匹配 kubeadm")
	manifest := f.String("manifest", "", "可选，本地 Calico 清单；默认 k8s.network.manifest，不改写")
	base := f.String("calico-download-base", defaults.Install.Calico.DownloadBase, "Calico 清单根地址；CALICO_DOWNLOAD_BASE / install.calico.download_base")
	kubeconfig := f.String("kubeconfig", "", "kubeconfig 文件；默认 KUBECONFIG 或 ~/.kube/config")
	clusterContext := f.String("context", "", "目标 context；默认 current-context")
	wait := f.Bool("wait", true, "等待 Calico DaemonSet 和控制器就绪")
	dry := f.Bool("dry-run", false, "仅预览，不连接集群、不下载或安装")
	timeout := f.Duration("timeout", 10*time.Minute, "预检、下载、安装和等待共用的总超时")
	bindCommand(cmd, func() (int, error) {
		if f.NArg() != 0 || *timeout <= 0 {
			return 2, errors.New("多余参数或 timeout 无效")
		}
		c, err := commandConfig(f, *config)
		if err != nil {
			return 2, err
		}
		setConfigFlag(f, "version", c.K8s.Network.Calico.Version)
		if *version == "" {
			*version = defaults.K8s.Network.Calico.Version
		}
		setConfigFlag(f, "pod-subnet", c.K8s.Network.PodSubnet)
		if *podCIDR == "" {
			*podCIDR = defaults.K8s.Network.PodSubnet
		}
		setConfigFlag(f, "manifest", c.K8s.Network.Manifest)
		setEnvFlag(f, "calico-download-base", "CALICO_DOWNLOAD_BASE")
		setConfigFlag(f, "calico-download-base", c.Install.Calico.DownloadBase)
		c.Install.Calico.DownloadBase = *base
		if c.Install.Calico.SHA256 == "" {
			c.Install.Calico.SHA256 = defaults.Install.Calico.SHA256
		}
		if c.Install.Calico.FallbackBases == nil {
			c.Install.Calico.FallbackBases = defaults.Install.Calico.FallbackBases
		}
		c.K8s.Network.Calico.Version = *version
		c.K8s.Network.PodSubnet = *podCIDR
		c.K8s.Network.Manifest = *manifest
		// Actual Kubernetes compatibility is checked against the server before applying.
		if err := validateClusterCalico(&c.K8s, 24); err != nil {
			return 2, err
		}
		if *manifest == "" {
			if _, err := clusterCalicoSource(c); err != nil {
				return 2, err
			}
		} else {
			raw, err := readClusterNetwork(*manifest)
			if err != nil {
				return 2, err
			}
			if err := validateStandaloneCalicoManifest(raw, *version, *podCIDR); err != nil {
				return 2, err
			}
		}
		fmt.Fprintf(out, "Calico %s；Pod CIDR=%s；等待就绪=%t\n", *version, *podCIDR, *wait)
		source := *manifest
		if source == "" {
			source, _ = clusterCalicoSource(c)
		}
		fmt.Fprintln(out, "清单:", source)
		if *dry {
			fmt.Fprintln(out, "仅预览；未连接集群、下载或安装。")
			return 0, nil
		}
		ctx, cancel := context.WithTimeout(parent, *timeout)
		defer cancel()
		kube, err := selectedKubectl(ctx, *kubeconfig, *clusterContext, out)
		if err != nil {
			return 3, err
		}
		if err := preflightStandaloneCalico(c.K8s, kube); err != nil {
			return 1, err
		}
		work, err := os.MkdirTemp("", "opskit-calico-")
		if err != nil {
			return 1, err
		}
		defer os.RemoveAll(work)
		if err := prepareClusterNetwork(ctx, &c, work, out); err != nil {
			return taskError(ctx, err)
		}
		// Use the same kubectl target and context for every read and mutation.
		raw, err := kube("apply", "-f", c.K8s.Network.Manifest)
		if err != nil {
			return taskError(ctx, err)
		}
		fmt.Fprint(diag, string(raw))
		if *wait {
			if err := waitClusterCalico(ctx, kube, out, *version); err != nil {
				return taskError(ctx, err)
			}
		} else {
			fmt.Fprintln(out, "Calico 清单已提交；未等待就绪。")
		}
		return 0, nil
	})
	return cmd, nil
}

func preflightStandaloneCalico(c clusterSettings, kube func(...string) ([]byte, error)) error {
	raw, err := kube("version", "-o", "json")
	if err != nil {
		return err
	}
	var server struct {
		ServerVersion struct {
			GitVersion string `json:"gitVersion"`
		} `json:"serverVersion"`
	}
	if err := json.Unmarshal(raw, &server); err != nil {
		return err
	}
	minor, err := kubeadmMinor(server.ServerVersion.GitVersion)
	if err != nil {
		return fmt.Errorf("无法验证实际 Kubernetes 版本 %s: %w", server.ServerVersion.GitVersion, err)
	}
	if err := validateClusterCalico(&c, minor); err != nil {
		return err
	}
	raw, err = kube("-n", "kube-system", "get", "configmap", "kubeadm-config", "-o", "json")
	if err != nil {
		return fmt.Errorf("需要可读取 kubeadm-config 的 kubeadm 集群: %w", err)
	}
	var cm struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(raw, &cm); err != nil {
		return err
	}
	var config struct {
		Networking struct {
			PodSubnet string `yaml:"podSubnet"`
		} `yaml:"networking"`
	}
	if err := yaml.Unmarshal([]byte(cm.Data["ClusterConfiguration"]), &config); err != nil {
		return err
	}
	if config.Networking.PodSubnet != c.Network.PodSubnet {
		return fmt.Errorf("Pod CIDR 不一致：kubeadm=%q，Calico=%q；未安装", config.Networking.PodSubnet, c.Network.PodSubnet)
	}
	raw, err = kube("get", "daemonsets", "-A", "-o", "json")
	if err != nil {
		return err
	}
	var workloads struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Image string `json:"image"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &workloads); err != nil {
		return err
	}
	for _, ds := range workloads.Items {
		for _, marker := range []string{"calico", "flannel", "cilium", "weave", "kube-router", "antrea", "canal"} {
			found := strings.Contains(strings.ToLower(ds.Metadata.Name), marker)
			for _, container := range ds.Spec.Template.Spec.Containers {
				found = found || strings.Contains(strings.ToLower(container.Image), marker)
			}
			if found {
				return fmt.Errorf("已有网络插件 DaemonSet %s，拒绝替换；此命令仅用于首次安装", ds.Metadata.Name)
			}
		}
	}
	return nil
}

// Local manifests are never rewritten: validate their pool and image version as supplied.
func validateStandaloneCalicoManifest(raw []byte, version, cidr string) error {
	if _, err := renderClusterCalico(raw, version, cidr); err != nil {
		return err
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	for {
		var doc map[string]any
		if err := decoder.Decode(&doc); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		metadata, _ := doc["metadata"].(map[string]any)
		if doc["kind"] != "DaemonSet" || metadata["name"] != "calico-node" {
			continue
		}
		spec, _ := doc["spec"].(map[string]any)
		template, _ := spec["template"].(map[string]any)
		pod, _ := template["spec"].(map[string]any)
		for _, item := range sliceField(pod, "containers") {
			container, _ := item.(map[string]any)
			if container["name"] != "calico-node" {
				continue
			}
			for _, item := range sliceField(container, "env") {
				setting, _ := item.(map[string]any)
				if setting["name"] == "CALICO_IPV4POOL_CIDR" && setting["value"] == cidr {
					return nil
				}
			}
		}
	}
	return errors.New("本地 Calico 清单的 CALICO_IPV4POOL_CIDR 必须显式匹配 --pod-subnet / k8s.network.kubePodsCIDR")
}
