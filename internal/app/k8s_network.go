package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func validateClusterCalico(c *clusterSettings, minor int) error {
	if c.Network.Calico.Version != "v3.26.5" {
		return errors.New("Calico 自动安装当前支持固定版本 v3.26.5；请设置 k8s.network.calico.version 或使用本地 network.manifest")
	}
	if minor < 24 || minor > 28 {
		return errors.New("Calico v3.26.5 自动安装仅支持 Kubernetes 1.24～1.28；其他组合请提供已验证的本地 network.manifest")
	}
	ip, pool, err := net.ParseCIDR(c.Network.PodSubnet)
	if err != nil || ip.To4() == nil {
		return errors.New("Calico 需要有效 IPv4 kubePodsCIDR")
	}
	prefix, _ := pool.Mask.Size()
	if prefix > 26 {
		return errors.New("Calico 默认 IPv4 分配块为 /26，kubePodsCIDR 前缀不能大于 /26")
	}
	if !ip.Equal(pool.IP) {
		return errors.New("Calico kubePodsCIDR 必须使用规范网络地址")
	}
	return nil
}
func clusterCalicoSource(c fileConfig) (string, error) {
	if err := validateKubeadmBase(c.Install.Calico.DownloadBase); err != nil {
		return "", fmt.Errorf("install.calico.download_base: %w", err)
	}
	hash, err := hex.DecodeString(c.Install.Calico.SHA256)
	if err != nil || len(hash) != sha256.Size {
		return "", errors.New("install.calico.sha256 必须为目标清单的 SHA-256")
	}
	return fmt.Sprintf("%s/projectcalico/calico/%s/manifests/calico.yaml", strings.TrimRight(c.Install.Calico.DownloadBase, "/"), c.K8s.Network.Calico.Version), nil
}
func readClusterNetwork(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 CNI 清单: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("CNI 清单为空")
	}
	return raw, nil
}
func prepareClusterNetwork(ctx context.Context, c *fileConfig, work string, out io.Writer) error {
	var raw []byte
	var err error
	if c.K8s.Network.Manifest != "" {
		raw, err = readClusterNetwork(c.K8s.Network.Manifest)
	} else {
		source, e := clusterCalicoSource(*c)
		if e != nil {
			return e
		}
		fmt.Fprintf(out, "下载 Calico %s 清单并校验 SHA-256: %s\n", c.K8s.Network.Calico.Version, source)
		downloads := c.Install.DownloadDir
		if downloads == "" {
			downloads, err = defaultDownloadDir()
			if err != nil {
				return err
			}
		}
		var b bytes.Buffer
		if err = downloadRetained(ctx, source, &b, downloads, "calico", c.K8s.Network.Calico.Version); err != nil {
			return err
		}
		sum := sha256.Sum256(b.Bytes())
		if !strings.EqualFold(hex.EncodeToString(sum[:]), c.Install.Calico.SHA256) {
			return errors.New("Calico 清单 SHA-256 不匹配；未修改节点")
		}
		raw, err = renderClusterCalico(b.Bytes(), c.K8s.Network.Calico.Version, c.K8s.Network.PodSubnet)
	}
	if err != nil {
		return err
	}
	target := filepath.Join(work, "network.yaml")
	if err = os.WriteFile(target, raw, 0600); err != nil {
		return err
	}
	c.K8s.Network.Manifest = target
	return nil
}

// Change only the node's IP-pool and autodetection settings; preserve CRDs and image names.
func renderClusterCalico(raw []byte, version, podCIDR string) ([]byte, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	nodes, controllers := 0, 0
	for {
		var doc map[string]any
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("Calico 清单 YAML 无效: %w", err)
		}
		if len(doc) == 0 {
			continue
		}
		metadata, _ := doc["metadata"].(map[string]any)
		name, _ := metadata["name"].(string)
		isNode := doc["kind"] == "DaemonSet" && name == "calico-node"
		isController := doc["kind"] == "Deployment" && name == "calico-kube-controllers"
		if isNode || isController {
			if metadata["namespace"] != "kube-system" {
				return nil, errors.New("Calico 工作负载命名空间不符合受支持清单")
			}
			spec, _ := doc["spec"].(map[string]any)
			template, _ := spec["template"].(map[string]any)
			pod, _ := template["spec"].(map[string]any)
			containers, _ := pod["containers"].([]any)
			found := false
			for _, item := range append(append([]any{}, containers...), sliceField(pod, "initContainers")...) {
				container, ok := item.(map[string]any)
				if !ok {
					return nil, errors.New("Calico container 配置无效")
				}
				image, _ := container["image"].(string)
				if !strings.HasPrefix(image, "docker.io/calico/") || !strings.HasSuffix(image, ":"+version) {
					return nil, fmt.Errorf("Calico 镜像版本不匹配: %s，要求 %s", image, version)
				}
				if isNode && container["name"] == "calico-node" {
					env := sliceField(container, "env")
					for _, setting := range []struct{ name, value string }{{"CALICO_IPV4POOL_CIDR", podCIDR}, {"IP_AUTODETECTION_METHOD", "kubernetes-internal-ip"}} {
						updated := false
						for i, item := range env {
							value, ok := item.(map[string]any)
							if ok && value["name"] == setting.name {
								env[i] = map[string]any{"name": setting.name, "value": setting.value}
								updated = true
							}
						}
						if !updated {
							env = append(env, map[string]any{"name": setting.name, "value": setting.value})
						}
					}
					container["env"] = env
					found = true
				}
				if isController && container["name"] == "calico-kube-controllers" {
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("Calico 清单缺少容器: %s", name)
			}
			if isNode {
				nodes++
			} else {
				controllers++
			}
		}
		if err = encoder.Encode(doc); err != nil {
			return nil, err
		}
	}
	if nodes != 1 || controllers != 1 {
		return nil, errors.New("Calico 清单需要唯一 calico-node 和 calico-kube-controllers 工作负载")
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
func sliceField(m map[string]any, key string) []any { value, _ := m[key].([]any); return value }

func waitClusterCalico(ctx context.Context, kube func(...string) ([]byte, error), out io.Writer, version string) error {
	for _, step := range [][]string{
		{"-n", "kube-system", "rollout", "status", "daemonset/calico-node"},
		{"-n", "kube-system", "wait", "--for=condition=Available", "deployment/calico-kube-controllers"},
	} {
		if err := ctx.Err(); err != nil {
			return err
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("Calico 等待缺少总期限")
		}
		args := append(step, "--timeout="+time.Until(deadline).String())
		// Watches share the total deadline rather than the per-request 30s read timeout.
		args = append(args, "--request-timeout=0")
		raw, err := kube(args...)
		if err != nil {
			return fmt.Errorf("等待 Calico %s 就绪: %w", version, err)
		}
		if _, err = out.Write(raw); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "Calico %s 就绪\n", version)
	return nil
}

func checkNewCalicoNetwork(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		ext := filepath.Ext(entry.Name())
		if ext == ".conf" || ext == ".conflist" || ext == ".json" {
			return fmt.Errorf("已有 CNI 配置 %s，拒绝自动替换为 Calico", filepath.Join(dir, entry.Name()))
		}
	}
	return nil
}
