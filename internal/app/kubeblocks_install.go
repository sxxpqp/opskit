package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func kubeBlocksArtifactFile(ctx context.Context, a kubeBlocksArtifact, local, downloads, name string) (string, func(), error) {
	var raw []byte
	var err error
	if local != "" {
		raw, err = os.ReadFile(local)
	} else {
		var b bytes.Buffer
		err = downloadRetained(ctx, a.URL, &b, downloads, "kubeblocks", name)
		raw = b.Bytes()
	}
	if err != nil {
		return "", func() {}, err
	}
	if err = checkRuntimeHash(raw, a.SHA256); err != nil {
		return "", func() {}, fmt.Errorf("KubeBlocks %s 摘要错误: %w", name, err)
	}
	f, err := os.CreateTemp("", "opskit-kubeblocks-"+name+"-*")
	if err != nil {
		return "", func() {}, err
	}
	clean := func() { os.Remove(f.Name()) }
	_, err = f.Write(raw)
	err = errors.Join(err, f.Close())
	if err != nil {
		clean()
		return "", func() {}, err
	}
	return f.Name(), clean, nil
}

// Initial installation only. No upgrade, deletion, CRD adoption or ownership takeover.
func installKubeBlocks(ctx context.Context, kube func(...string) ([]byte, error), c kubeBlocksSettings, downloads, chart, crds, addon, kubeconfig, clusterContext string, out, diag io.Writer) error {
	if _, err := commandOutput(ctx, nil, "helm", "version", "--short"); err != nil {
		return fmt.Errorf("需要 Helm 3.10+ 和 kubectl: %w", err)
	}
	raw, err := kube("get", "crd", "-o", "json")
	if err != nil {
		return err
	}
	var list struct {
		Items []struct{ Metadata struct{ Name string } }
	}
	if err = json.Unmarshal(raw, &list); err != nil {
		return err
	}
	for _, item := range list.Items {
		if strings.HasSuffix(item.Metadata.Name, ".kubeblocks.io") {
			return errors.New("存在 KubeBlocks CRD；install 仅首次安装，拒绝接管或自动升级。请检查已有 Operator/部分安装")
		}
	}
	base := []string{}
	if kubeconfig != "" {
		base = append(base, "--kubeconfig", kubeconfig)
	}
	base = append(base, "--kube-context", clusterContext)
	releases, err := commandOutput(ctx, nil, "helm", append(append([]string{}, base...), "list", "--all-namespaces", "--all", "--output", "json")...)
	if err != nil {
		return err
	}
	var existing []struct{ Name, Chart string }
	if err = json.Unmarshal(releases, &existing); err != nil {
		return err
	}
	for _, release := range existing {
		if release.Name == "kubeblocks" || strings.HasPrefix(release.Chart, "kubeblocks-") {
			return errors.New("已有 KubeBlocks Helm Release；拒绝重复安装或自动升级")
		}
	}
	// Helm checks named RBAC/admission resources before creating them; never use take-ownership.
	if downloads == "" {
		downloads, err = defaultDownloadDir()
		if err != nil {
			return err
		}
	}
	if err = os.MkdirAll(downloads, 0700); err != nil {
		return err
	}
	operator, cleanup, err := kubeBlocksArtifactFile(ctx, c.Chart, chart, downloads, "operator.tgz")
	if err != nil {
		return err
	}
	defer cleanup()
	definitions, cleanup, err := kubeBlocksArtifactFile(ctx, c.CRDs, crds, downloads, "crds.yaml")
	if err != nil {
		return err
	}
	defer cleanup()
	minio, cleanup, err := kubeBlocksArtifactFile(ctx, c.MinIOAddon, addon, downloads, "minio.tgz")
	if err != nil {
		return err
	}
	defer cleanup()
	// Validate every downloaded object before the first cluster mutation.
	if err = validateKubeBlocksCRDs(definitions); err != nil {
		return err
	}
	if _, err = kube("create", "--save-config=false", "-f", definitions, "--dry-run=server"); err != nil {
		return err
	}
	if _, err = kube("create", "--save-config=false", "-f", definitions); err != nil {
		return fmt.Errorf("CRD 创建失败；保留现场: %w", err)
	}
	if _, err = kube("wait", "--for=condition=Established", "--timeout=120s", "-f", definitions); err != nil {
		return err
	}
	run := func(args ...string) error {
		return commandRun(ctx, nil, out, diag, "helm", append(append([]string{}, base...), args...)...)
	}
	ttl := "15m"
	if err = run("install", "kubeblocks", operator, "--namespace", c.OperatorNamespace, "--create-namespace", "--skip-crds", "--set", "crd.enabled=false", "--set", "autoInstalledAddons={mysql,redis}", "--set", "replicaCount=2", "--set", "storageClass.create=false", "--wait", "--timeout", ttl); err != nil {
		return fmt.Errorf("Operator 安装失败，保留资源排查；不自动卸载: %w", err)
	}
	if err = run("install", "kb-addon-minio", minio, "--namespace", c.OperatorNamespace, "--wait", "--timeout", ttl); err != nil {
		return err
	}
	for _, e := range []kubeBlocksEngine{c.MySQL, c.Redis, c.MinIO} {
		for {
			raw, err = kube("get", "componentdefinitions.apps.kubeblocks.io", e.ComponentDef, "--ignore-not-found", "-o", "name")
			if err != nil {
				return err
			}
			if len(bytes.TrimSpace(raw)) > 0 {
				break
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("等待 ComponentDefinition %s: %w", e.ComponentDef, ctx.Err())
			case <-time.After(5 * time.Second):
			}
		}
	}
	fmt.Fprintln(out, "KubeBlocks Operator 与 MySQL/Redis/MinIO 定义已就绪；尚未创建业务集群。")
	return nil
}

func validateKubeBlocksCRDs(path string) error {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	count := 0
	seen := map[string]bool{}
	for {
		var obj struct {
			APIVersion string `yaml:"apiVersion"`
			Kind       string `yaml:"kind"`
			Metadata   struct{ Name string }
			Spec       struct{ Group string }
		}
		err = dec.Decode(&obj)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if obj.Kind == "" {
			continue
		}
		if obj.APIVersion != "apiextensions.k8s.io/v1" || obj.Kind != "CustomResourceDefinition" || !strings.HasSuffix(obj.Spec.Group, ".kubeblocks.io") || seen[obj.Metadata.Name] {
			return errors.New("CRD 包含非 KubeBlocks CRD 或重复对象")
		}
		seen[obj.Metadata.Name] = true
		count++
	}
	if count == 0 || !seen["clusters.apps.kubeblocks.io"] || !seen["addons.extensions.kubeblocks.io"] {
		return errors.New("CRD 清单不完整")
	}
	return nil
}
