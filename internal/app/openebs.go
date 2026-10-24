package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type openEBSSettings struct {
	Manifest   string `yaml:"manifest"`
	SetDefault bool   `yaml:"set_default"`
}

type openEBSResource struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string            `yaml:"name"`
		Namespace   string            `yaml:"namespace"`
		Annotations map[string]string `yaml:"annotations"`
	} `yaml:"metadata"`
	Provisioner       string `yaml:"provisioner"`
	VolumeBindingMode string `yaml:"volumeBindingMode"`
}

func newOpenEBSInstallCommand(parent context.Context, out, diag io.Writer) (*cobra.Command, error) {
	var defaults fileConfig
	if err := yamlClusterDefaults(&defaults); err != nil {
		return nil, err
	}
	cmd := &cobra.Command{Use: "install", Long: `通过官方 3.x Operator YAML 安装 NDM 和 Local PV，不使用 Helm。
固定 openebs namespace；不安装 cStor、Jiva、Mayastor，不格式化或自动分配裸盘。
仅用于首次安装，拒绝覆盖已有同名资源；默认不修改默认 StorageClass。
--set-default 显式设置 openebs-hostpath；存在其他默认类时拒绝。
下载失败或单源超时后使用 install.openebs.fallback_urls；摘要不匹配立即停止。
--dry-run 仅检查本地配置和本地清单，不连接集群、不下载。`}
	f := cmd.Flags()
	config := f.String("config", defaultConfigPath, "统一 YAML 配置")
	manifest := f.String("manifest", "", "本地 Operator YAML；默认 openebs.manifest，不改写")
	source := f.String("operator-url", defaults.Install.OpenEBS.OperatorURL, "Operator YAML 地址；OPENEBS_OPERATOR_URL / install.openebs.operator_url")
	checksum := f.String("sha256", defaults.Install.OpenEBS.SHA256, "下载清单 SHA-256；install.openebs.sha256，下载时必填")
	setDefault := f.Bool("set-default", false, "将 openebs-hostpath 设为默认；openebs.set_default")
	kubeconfig := f.String("kubeconfig", "", "kubeconfig 路径；默认遵循 kubectl")
	clusterContext := f.String("context", "", "Kubernetes context；默认固定 current-context")
	wait := f.Bool("wait", true, "等待 CRD、Deployment 和 DaemonSet 就绪")
	dry := f.Bool("dry-run", false, "仅预览，不下载、不连接集群")
	timeout := f.Duration("timeout", 15*time.Minute, "下载、预检、安装与等待共用的总超时")
	bindCommand(cmd, func() (int, error) {
		if f.NArg() != 0 || *timeout <= 0 {
			return 2, errors.New("参数或 timeout 无效")
		}
		c, err := commandConfig(f, *config)
		if err != nil {
			return 2, err
		}
		setConfigFlag(f, "manifest", c.OpenEBS.Manifest)
		setConfigFlag(f, "set-default", strconv.FormatBool(c.OpenEBS.SetDefault))
		setEnvFlag(f, "operator-url", "OPENEBS_OPERATOR_URL")
		setConfigFlag(f, "operator-url", c.Install.OpenEBS.OperatorURL)
		setConfigFlag(f, "sha256", c.Install.OpenEBS.SHA256)
		if c.Install.OpenEBS.FallbackURLs == nil {
			c.Install.OpenEBS.FallbackURLs = defaults.Install.OpenEBS.FallbackURLs
		}
		var raw []byte
		if *manifest != "" {
			raw, err = os.ReadFile(*manifest)
			if err == nil {
				_, err = validateOpenEBSManifest(raw)
			}
		} else {
			err = validateKubeadmBase(*source)
			_, shaErr := hex.DecodeString(*checksum)
			if err == nil && (len(*checksum) != 64 || shaErr != nil) {
				err = errors.New("install.openebs.sha256 必须是下载清单的 SHA-256")
			}
			for _, fallback := range c.Install.OpenEBS.FallbackURLs {
				if e := validateKubeadmBase(fallback); e != nil {
					return 2, fmt.Errorf("install.openebs.fallback_urls: %w", e)
				}
			}
		}
		if err != nil {
			return 2, err
		}
		fmt.Fprintf(out, "OpenEBS 3.x Operator；namespace=openebs；默认存储类=%t；等待就绪=%t\n", *setDefault, *wait)
		if *manifest != "" {
			fmt.Fprintln(out, "本地清单:", *manifest)
		} else {
			fmt.Fprintln(out, "清单下载地址:", *source)
		}
		if *dry {
			return 0, nil
		}
		ctx, cancel := context.WithTimeout(parent, *timeout)
		defer cancel()
		if *manifest == "" {
			downloads := c.Install.DownloadDir
			if downloads == "" {
				downloads, err = defaultDownloadDir()
			}
			if err != nil {
				return 1, err
			}
			raw, err = downloadOpenEBSOperator(ctx, append([]string{*source}, c.Install.OpenEBS.FallbackURLs...), downloads, *checksum, time.Minute, out)
			if err != nil {
				return taskError(ctx, err)
			}
		}
		resources, err := validateOpenEBSManifest(raw)
		if err != nil {
			return 1, err
		}
		kube, err := selectedKubectl(ctx, *kubeconfig, *clusterContext, out)
		if err != nil {
			return 3, err
		}
		if err := preflightOpenEBS(kube, resources, *setDefault); err != nil {
			return 1, err
		}
		file, err := os.CreateTemp("", "opskit-openebs-*.yaml")
		if err != nil {
			return 1, err
		}
		defer os.Remove(file.Name())
		_, writeErr := file.Write(raw)
		closeErr := file.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return 1, err
		}
		result, err := kube("apply", "-f", file.Name())
		fmt.Fprint(diag, string(result))
		if err != nil {
			return taskError(ctx, err)
		}
		if *wait {
			if err := waitOpenEBS(ctx, kube, resources, out); err != nil {
				return taskError(ctx, err)
			}
		}
		if *setDefault {
			if err := checkOpenEBSDefault(kube); err != nil {
				return 1, err
			}
			patch := `{"metadata":{"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}`
			if _, err := kube("patch", "storageclass", "openebs-hostpath", "--type=merge", "-p", patch); err != nil {
				return taskError(ctx, err)
			}
		}
		if *wait {
			fmt.Fprintln(out, "OpenEBS Operator 与 Local PV 工作负载已就绪；StorageClass: openebs-hostpath（节点本地存储）。")
		} else {
			fmt.Fprintln(out, "OpenEBS Operator 清单已提交；未等待就绪。")
		}
		return 0, nil
	})
	return cmd, nil
}

func downloadOpenEBSOperator(ctx context.Context, sources []string, downloads, checksum string, perSource time.Duration, out io.Writer) ([]byte, error) {
	seen := map[string]bool{}
	var failures []error
	for _, source := range sources {
		if seen[source] {
			continue
		}
		seen[source] = true
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var body bytes.Buffer
		attempt, cancel := context.WithTimeout(ctx, perSource)
		err := downloadRetained(attempt, source, &body, downloads, "openebs", "operator")
		cancel()
		if err == nil {
			if err := checkRuntimeHash(body.Bytes(), checksum); err != nil {
				return nil, fmt.Errorf("OpenEBS Operator SHA-256 校验失败: %s: %w", source, err)
			}
			fmt.Fprintln(out, "OpenEBS 清单下载校验通过:", source)
			return body.Bytes(), nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		failures = append(failures, fmt.Errorf("%s: %w", source, err))
		fmt.Fprintf(out, "OpenEBS 源下载失败或超时: %s；尝试下一个配置源（如有）\n", source)
	}
	return nil, fmt.Errorf("OpenEBS 所有下载源失败: %w", errors.Join(failures...))
}

func validateOpenEBSManifest(raw []byte) ([]openEBSResource, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	seen := map[string]bool{}
	var resources []openEBSResource
	for {
		var resource openEBSResource
		if err := decoder.Decode(&resource); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if resource.Kind == "" && resource.APIVersion == "" {
			continue
		}
		key := resource.Kind + "/" + resource.Metadata.Name
		if resource.Metadata.Name == "" || seen[key] || (resource.Metadata.Namespace != "" && resource.Metadata.Namespace != "openebs") {
			return nil, fmt.Errorf("OpenEBS 清单对象名称、namespace 或重复对象无效: %s", key)
		}
		switch resource.Kind {
		case "Namespace":
			if resource.Metadata.Name != "openebs" {
				return nil, errors.New("Operator 清单只支持 openebs namespace")
			}
		case "ServiceAccount", "ConfigMap", "DaemonSet", "Deployment", "Service":
			if resource.Metadata.Namespace != "openebs" {
				return nil, fmt.Errorf("%s 必须显式位于 openebs namespace", key)
			}
		case "ClusterRole", "ClusterRoleBinding", "CustomResourceDefinition", "StorageClass":
			if resource.Metadata.Namespace != "" {
				return nil, fmt.Errorf("集群级对象 %s 不得设置 namespace", key)
			}
		default:
			return nil, fmt.Errorf("Operator 清单不支持对象 %s", resource.Kind)
		}
		if resource.Kind == "StorageClass" {
			for _, annotation := range []string{"storageclass.kubernetes.io/is-default-class", "storageclass.beta.kubernetes.io/is-default-class"} {
				if resource.Metadata.Annotations[annotation] == "true" {
					return nil, errors.New("清单不得直接设置默认 StorageClass；请使用 --set-default")
				}
			}
			if resource.Metadata.Name == "openebs-hostpath" && (resource.Provisioner != "openebs.io/local" || resource.VolumeBindingMode != "WaitForFirstConsumer") {
				return nil, errors.New("openebs-hostpath 必须使用 openebs.io/local 和 WaitForFirstConsumer")
			}
		}
		seen[key] = true
		resources = append(resources, resource)
	}
	for _, required := range []string{"Namespace/openebs", "Deployment/openebs-ndm-operator", "Deployment/openebs-localpv-provisioner", "DaemonSet/openebs-ndm", "CustomResourceDefinition/blockdevices.openebs.io", "CustomResourceDefinition/blockdeviceclaims.openebs.io", "StorageClass/openebs-hostpath"} {
		if !seen[required] {
			return nil, fmt.Errorf("OpenEBS Operator 清单缺少 %s", required)
		}
	}
	return resources, nil
}

func preflightOpenEBS(kube func(...string) ([]byte, error), resources []openEBSResource, setDefault bool) error {
	if _, err := kube("get", "--raw=/readyz"); err != nil {
		return err
	}
	for _, resource := range resources {
		if resource.Kind == "Namespace" {
			continue // An otherwise empty namespace can be prepared in advance.
		}
		args := []string{"get", strings.ToLower(resource.Kind) + "/" + resource.Metadata.Name, "--ignore-not-found", "-o", "name"}
		if resource.Metadata.Namespace != "" {
			args = append(args, "-n", resource.Metadata.Namespace)
		}
		raw, err := kube(args...)
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(raw)) != 0 {
			return fmt.Errorf("已有 OpenEBS 或同名资源 %s/%s，拒绝覆盖；仅支持首次安装", resource.Kind, resource.Metadata.Name)
		}
	}
	if setDefault {
		return checkOpenEBSDefault(kube)
	}
	return nil
}

func checkOpenEBSDefault(kube func(...string) ([]byte, error)) error {
	raw, err := kube("get", "storageclasses", "-o", "json")
	if err != nil {
		return err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name        string            `json:"name"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return err
	}
	for _, item := range list.Items {
		for _, annotation := range []string{"storageclass.kubernetes.io/is-default-class", "storageclass.beta.kubernetes.io/is-default-class"} {
			if item.Metadata.Annotations[annotation] == "true" && item.Metadata.Name != "openebs-hostpath" {
				return fmt.Errorf("已有默认 StorageClass %s，未修改；请先自行选择默认存储策略", item.Metadata.Name)
			}
		}
	}
	return nil
}

func waitOpenEBS(ctx context.Context, kube func(...string) ([]byte, error), resources []openEBSResource, out io.Writer) error {
	for _, resource := range resources {
		var args []string
		switch resource.Kind {
		case "CustomResourceDefinition":
			args = []string{"wait", "--for=condition=Established", "crd/" + resource.Metadata.Name}
		case "Deployment", "DaemonSet":
			args = []string{"-n", "openebs", "rollout", "status", strings.ToLower(resource.Kind) + "/" + resource.Metadata.Name}
		default:
			continue
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("OpenEBS 等待缺少总期限")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		args = append(args, "--request-timeout=0", "--timeout="+time.Until(deadline).String())
		raw, err := kube(args...)
		fmt.Fprint(out, string(raw))
		if err != nil {
			return err
		}
	}
	return nil
}
