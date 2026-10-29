package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	templates "github.com/sxxpqp/opskit"
	"gopkg.in/yaml.v3"
)

type kubeBlocksEngine struct {
	ComponentDef string `yaml:"component_def,omitempty"`
	Version      string `yaml:"version,omitempty"`
	CPU          string `yaml:"cpu,omitempty"`
	Memory       string `yaml:"memory,omitempty"`
	LimitCPU     string `yaml:"limit_cpu,omitempty"`
	LimitMemory  string `yaml:"limit_memory,omitempty"`
}
type kubeBlocksArtifact struct {
	URL    string `yaml:"url,omitempty"`
	SHA256 string `yaml:"sha256,omitempty"`
}
type kubeBlocksSettings struct {
	Name              string             `yaml:"name,omitempty"`
	Namespace         string             `yaml:"namespace,omitempty"`
	OperatorNamespace string             `yaml:"operator_namespace,omitempty"`
	Engine            string             `yaml:"engine,omitempty"`
	StorageClass      string             `yaml:"storage_class,omitempty"`
	StorageSize       string             `yaml:"storage_size,omitempty"`
	AllowColocation   bool               `yaml:"allow_colocation,omitempty"`
	CRDs              kubeBlocksArtifact `yaml:"crds,omitempty"`
	Chart             kubeBlocksArtifact `yaml:"chart,omitempty"`
	MinIOAddon        kubeBlocksArtifact `yaml:"minio_addon,omitempty"`
	MySQL             kubeBlocksEngine   `yaml:"mysql,omitempty"`
	Redis             kubeBlocksEngine   `yaml:"redis,omitempty"`
	MinIO             kubeBlocksEngine   `yaml:"minio,omitempty"`
}

func kubeBlocksDefaults(c kubeBlocksSettings) (kubeBlocksSettings, error) {
	var base struct {
		KubeBlocks kubeBlocksSettings `yaml:"kubeblocks"`
	}
	if err := yaml.Unmarshal(templates.ConfigTemplate, &base); err != nil {
		return c, err
	}
	raw, err := yaml.Marshal(c)
	if err != nil {
		return c, err
	}
	err = yaml.Unmarshal(raw, &base.KubeBlocks)
	return base.KubeBlocks, err
}
func (c kubeBlocksSettings) component() kubeBlocksEngine {
	switch c.Engine {
	case "mysql":
		return c.MySQL
	case "redis":
		return c.Redis
	default:
		return c.MinIO
	}
}
func (c kubeBlocksSettings) replicas() int {
	switch c.Engine {
	case "mysql":
		return 3
	case "redis":
		return 6
	default:
		return 4
	}
}
func (c kubeBlocksSettings) clusterName() string { return c.Name + "-" + c.Engine }
func (c kubeBlocksSettings) validate() error {
	dns := regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$|^[a-z]$`)
	for _, s := range []string{c.Name, c.Namespace, c.OperatorNamespace} {
		if len(s) > 40 || !dns.MatchString(s) {
			return errors.New("kubeblocks 名称和 namespace 须为 1～40 位 DNS 标签")
		}
	}
	if c.Namespace == c.OperatorNamespace || c.Namespace == "kube-system" || c.Namespace == "default" {
		return errors.New("中间件必须使用独立业务 namespace")
	}
	if c.Engine != "mysql" && c.Engine != "redis" && c.Engine != "minio" {
		return errors.New("kubeblocks.engine 必须是 mysql/redis/minio")
	}
	if c.StorageClass == "" || c.StorageSize == "" {
		return errors.New("必须显式配置 kubeblocks.storage_class 和 storage_size")
	}
	e := c.component()
	if e.ComponentDef == "" || e.Version == "" {
		return errors.New("必须固定 component_def 和 version")
	}
	q := regexp.MustCompile(`^[1-9][0-9]*(\.[0-9]+)?(m|Ki|Mi|Gi|Ti)?$`)
	for _, s := range []string{c.StorageSize, e.CPU, e.Memory, e.LimitCPU, e.LimitMemory} {
		if !q.MatchString(s) {
			return fmt.Errorf("无效的正资源数量: %s", s)
		}
	}
	return nil
}
func renderKubeBlocks(c kubeBlocksSettings) ([]byte, error) {
	e := c.component()
	component := map[string]any{"name": c.Engine, "componentDef": e.ComponentDef, "serviceVersion": e.Version, "replicas": c.replicas(), "disableExporter": true,
		"resources":            map[string]any{"requests": map[string]string{"cpu": e.CPU, "memory": e.Memory}, "limits": map[string]string{"cpu": e.LimitCPU, "memory": e.LimitMemory}},
		"volumeClaimTemplates": []any{map[string]any{"name": "data", "spec": map[string]any{"storageClassName": c.StorageClass, "accessModes": []string{"ReadWriteOnce"}, "resources": map[string]any{"requests": map[string]string{"storage": c.StorageSize}}}}}}
	term := map[string]any{"topologyKey": "kubernetes.io/hostname", "labelSelector": map[string]any{"matchLabels": map[string]string{"app.kubernetes.io/instance": c.clusterName()}}}
	anti := map[string]any{"requiredDuringSchedulingIgnoredDuringExecution": []any{term}}
	if c.AllowColocation {
		anti = map[string]any{"preferredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{"weight": 100, "podAffinityTerm": term}}}
	}
	spec := map[string]any{"terminationPolicy": "DoNotTerminate", "schedulingPolicy": map[string]any{"affinity": map[string]any{"podAntiAffinity": anti}}}
	if c.Engine == "redis" {
		component["replicas"] = 2
		spec["shardings"] = []any{map[string]any{"name": "shard", "shards": 3, "template": component}}
	} else {
		spec["componentSpecs"] = []any{component}
	}
	canonical, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(canonical)
	return yaml.Marshal(map[string]any{"apiVersion": "apps.kubeblocks.io/v1", "kind": "Cluster", "metadata": map[string]any{"name": c.clusterName(), "namespace": c.Namespace, "labels": map[string]string{"app.kubernetes.io/managed-by": "opskit"}, "annotations": map[string]string{"opskit.io/spec-sha256": hex.EncodeToString(sum[:])}}, "spec": spec})
}

func newKubeBlocksCommand(parent context.Context, action string, out, diag io.Writer) (*cobra.Command, error) {
	cmd := &cobra.Command{Use: action}
	f := cmd.Flags()
	config := f.String("config", defaultConfigPath, "统一 YAML 配置，读取 kubeblocks")
	engine := f.String("engine", "", "mysql / redis / minio")
	kubeconfig := f.String("kubeconfig", "", "kubeconfig 路径")
	clusterContext := f.String("context", "", "固定 Kubernetes context")
	dry := f.Bool("dry-run", false, "本地检查与预览，不联网或写集群")
	timeout := f.Duration("timeout", 20*time.Minute, "操作总超时")
	var chart, crds, addon *string
	if action == "install" {
		chart = f.String("chart", "", "本地固定版 Operator Chart，仍校验配置摘要")
		crds = f.String("crds", "", "本地固定版 CRD 清单，仍校验摘要")
		addon = f.String("minio-addon", "", "本地固定版 MinIO addon Chart，仍校验摘要")
	}
	bindCommand(cmd, func() (int, error) {
		if f.NArg() != 0 || *timeout <= 0 {
			return 2, errors.New("多余参数或 timeout 无效")
		}
		file, err := commandConfig(f, *config)
		if err != nil {
			return 2, err
		}
		c, err := kubeBlocksDefaults(file.KubeBlocks)
		if err != nil {
			return 2, err
		}
		if *engine != "" {
			c.Engine = *engine
		}
		if err = c.validate(); err != nil {
			return 2, err
		}
		raw, err := renderKubeBlocks(c)
		if err != nil {
			return 2, err
		}
		if action == "render" {
			_, err = out.Write(raw)
			return 0, err
		}
		if action == "install" {
			for _, a := range []kubeBlocksArtifact{c.CRDs, c.Chart, c.MinIOAddon} {
				if err = validateKubeadmBase(a.URL); err != nil {
					return 2, err
				}
				if b, e := hex.DecodeString(a.SHA256); e != nil || len(b) != 32 {
					return 2, errors.New("kubeblocks 下载必须配置 SHA-256")
				}
			}
		}
		if *dry {
			fmt.Fprintf(out, "KubeBlocks %s: engine=%s cluster=%s namespace=%s storage=%s nodes>=%d allow_colocation=%t\n", action, c.Engine, c.clusterName(), c.Namespace, c.StorageClass, c.replicas(), c.AllowColocation)
			return 0, nil
		}
		ctx, cancel := context.WithTimeout(parent, *timeout)
		defer cancel()
		// Resolve current-context once for both kubectl and Helm.
		if *clusterContext == "" {
			args := []string{}
			if *kubeconfig != "" {
				args = append(args, "--kubeconfig", *kubeconfig)
			}
			b, e := commandOutput(ctx, nil, "kubectl", append(args, "config", "current-context")...)
			if e != nil {
				return 3, e
			}
			*clusterContext = strings.TrimSpace(string(b))
			if *clusterContext == "" {
				return 2, errors.New("current-context 为空")
			}
		}
		kube, err := selectedKubectl(ctx, *kubeconfig, *clusterContext, out)
		if err != nil {
			return 3, err
		}
		if action == "install" {
			return taskError(ctx, installKubeBlocks(ctx, kube, c, file.Install.DownloadDir, *chart, *crds, *addon, *kubeconfig, *clusterContext, out, diag))
		}
		if action == "status" {
			return taskError(ctx, kubeBlocksHealthy(kube, c, out))
		}
		if err = preflightKubeBlocks(kube, c); err != nil {
			return 1, err
		}
		previous, err := kube("get", "clusters.apps.kubeblocks.io", c.clusterName(), "-n", c.Namespace, "--ignore-not-found", "-o", "json")
		if err != nil {
			return 1, err
		}
		if len(bytes.TrimSpace(previous)) > 0 {
			if err = kubeBlocksOwned(previous, raw); err != nil {
				return 1, err
			}
			fmt.Fprintln(out, "已有匹配归属与配置，继续等待；不修改 Cluster 或凭据。")
		} else {
			if err = kubeBlocksCreate(kube, raw, true); err != nil {
				return 1, err
			}
			if err = kubeBlocksCreate(kube, raw, false); err != nil {
				return 1, err
			}
		}
		var last error
		fmt.Fprintf(out, "等待 Cluster、Pod 和 PVC 就绪，最长 %s；失败保留资源。\n", *timeout)
		lastReport := time.Now()
		for {
			last = kubeBlocksHealthy(kube, c, io.Discard)
			if last == nil {
				return 0, kubeBlocksHealthy(kube, c, out)
			}
			if time.Since(lastReport) >= 30*time.Second {
				fmt.Fprintln(out, "仍在等待:", last)
				lastReport = time.Now()
			}
			select {
			case <-ctx.Done():
				return 1, fmt.Errorf("等待超时；保留 Cluster/PVC: %w；%v", ctx.Err(), last)
			case <-time.After(5 * time.Second):
			}
		}
	})
	return cmd, nil
}

func kubeBlocksCreate(kube func(...string) ([]byte, error), raw []byte, dry bool) error {
	f, err := os.CreateTemp("", "opskit-kubeblocks-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(raw)
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	args := []string{"create", "--save-config=false", "-f", f.Name()}
	if dry {
		args = append(args, "--dry-run=server")
	}
	_, err = kube(args...)
	return err
}
func kubeBlocksOwned(actual, desired []byte) error {
	var a struct {
		Metadata struct{ Labels, Annotations map[string]string }
		Spec     map[string]any
	}
	var d struct {
		Metadata struct{ Labels, Annotations map[string]string }
		Spec     map[string]any
	}
	if err := json.Unmarshal(actual, &a); err != nil {
		return err
	}
	if err := yaml.Unmarshal(desired, &d); err != nil {
		return err
	}
	if a.Metadata.Labels["app.kubernetes.io/managed-by"] != "opskit" || a.Metadata.Annotations["opskit.io/spec-sha256"] != d.Metadata.Annotations["opskit.io/spec-sha256"] {
		return errors.New("Cluster 已存在且归属或配置不匹配；拒绝接管、升级或扩缩容")
	}
	// API defaulted extra fields are allowed; an external change to any desired field is not.
	if !kubeBlocksContains(a.Spec, d.Spec) {
		return errors.New("Cluster spec 已被外部修改；拒绝覆盖")
	}
	return nil
}
func kubeBlocksContains(actual, desired any) bool {
	switch d := desired.(type) {
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range d {
			if !kubeBlocksContains(a[k], v) {
				return false
			}
		}
		return true
	case []any:
		a, ok := actual.([]any)
		if !ok || len(a) != len(d) {
			return false
		}
		for i := range d {
			if !kubeBlocksContains(a[i], d[i]) {
				return false
			}
		}
		return true
	default:
		return fmt.Sprint(actual) == fmt.Sprint(desired)
	}
}

func preflightKubeBlocks(kube func(...string) ([]byte, error), c kubeBlocksSettings) error {
	for _, args := range [][]string{{"get", "namespace", c.Namespace}, {"get", "storageclass", c.StorageClass}, {"get", "componentdefinitions.apps.kubeblocks.io", c.component().ComponentDef}} {
		if _, err := kube(args...); err != nil {
			return err
		}
	}
	raw, err := kube("get", "nodes", "-o", "json")
	if err != nil {
		return err
	}
	var nodes struct {
		Items []struct {
			Metadata struct{ Labels map[string]string }
			Spec     struct {
				Unschedulable bool
				Taints        []struct{ Effect string }
			}
			Status struct {
				Conditions []struct{ Type, Status string }
			}
		}
	}
	if err = json.Unmarshal(raw, &nodes); err != nil {
		return err
	}
	n := 0
	for _, node := range nodes.Items {
		usable := !node.Spec.Unschedulable && node.Metadata.Labels["kubernetes.io/os"] == "linux"
		for _, t := range node.Spec.Taints {
			if t.Effect == "NoSchedule" || t.Effect == "NoExecute" {
				usable = false
			}
		}
		for _, cond := range node.Status.Conditions {
			if usable && cond.Type == "Ready" && cond.Status == "True" {
				n++
			}
		}
	}
	required := c.replicas()
	if c.AllowColocation {
		required = 1
	}
	if n < required {
		return fmt.Errorf("可调度 Ready 节点 %d，至少需要 %d；实验环境可显式 allow_colocation，但不构成主机故障隔离", n, required)
	}
	return nil
}

func kubeBlocksHealthy(kube func(...string) ([]byte, error), c kubeBlocksSettings, out io.Writer) error {
	raw, err := kube("get", "clusters.apps.kubeblocks.io", c.clusterName(), "-n", c.Namespace, "-o", "json")
	if err != nil {
		return err
	}
	desired, err := renderKubeBlocks(c)
	if err != nil {
		return err
	}
	if err = kubeBlocksOwned(raw, desired); err != nil {
		return err
	}
	var cluster struct {
		Metadata struct{ Generation int64 }
		Status   struct {
			Phase              string
			ObservedGeneration int64
		}
	}
	if err = json.Unmarshal(raw, &cluster); err != nil {
		return err
	}
	if cluster.Status.Phase != "Running" || cluster.Status.ObservedGeneration < cluster.Metadata.Generation {
		return fmt.Errorf("Cluster phase=%s observedGeneration=%d", cluster.Status.Phase, cluster.Status.ObservedGeneration)
	}
	selector := "app.kubernetes.io/instance=" + c.clusterName()
	raw, err = kube("get", "pods", "-n", c.Namespace, "-l", selector, "-o", "json")
	if err != nil {
		return err
	}
	var pods struct {
		Items []struct {
			Metadata struct {
				Name              string
				DeletionTimestamp *string
			}
			Spec   struct{ NodeName string }
			Status struct {
				Phase      string
				Conditions []struct{ Type, Status string }
			}
		}
	}
	if err = json.Unmarshal(raw, &pods); err != nil {
		return err
	}
	ready := 0
	nodeNames := map[string]bool{}
	for _, p := range pods.Items {
		if p.Status.Phase == "Succeeded" {
			continue
		}
		ok := false
		for _, cond := range p.Status.Conditions {
			if cond.Type == "Ready" && cond.Status == "True" {
				ok = true
			}
		}
		if !ok || p.Metadata.DeletionTimestamp != nil {
			return fmt.Errorf("Pod %s 未就绪", p.Metadata.Name)
		}
		ready++
		nodeNames[p.Spec.NodeName] = true
	}
	if ready < c.replicas() {
		return fmt.Errorf("Ready Pods=%d，至少需要 %d", ready, c.replicas())
	}
	if !c.AllowColocation && len(nodeNames) < c.replicas() {
		return errors.New("实例未分布到足够节点")
	}
	raw, err = kube("get", "pvc", "-n", c.Namespace, "-l", selector, "-o", "json")
	if err != nil {
		return err
	}
	var claims struct {
		Items []struct{ Status struct{ Phase string } }
	}
	if err = json.Unmarshal(raw, &claims); err != nil {
		return err
	}
	if len(claims.Items) < c.replicas() {
		return errors.New("PVC 数量不足")
	}
	for _, p := range claims.Items {
		if p.Status.Phase != "Bound" {
			return errors.New("存在未 Bound 的 PVC")
		}
	}
	fmt.Fprintf(out, "KubeBlocks %s Running；Ready Pods=%d，Bound PVC=%d，节点=%d，allow_colocation=%t\n", c.clusterName(), ready, len(claims.Items), len(nodeNames), c.AllowColocation)
	fmt.Fprintln(out, "仅报告控制器、Pod 和存储状态；业务读写与故障恢复需要另外验收。凭据由 Operator 生成并保存在 Secret，不输出密码。")
	return nil
}
