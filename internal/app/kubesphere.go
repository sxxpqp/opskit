package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	templates "github.com/sxxpqp/opskit"
	"gopkg.in/yaml.v3"
)

func runKubeSphere(parent context.Context, args []string, out, diag io.Writer) (int, error) {
	var templateDefaults fileConfig
	if err := yaml.Unmarshal(templates.ConfigTemplate, &templateDefaults); err != nil {
		return 1, err
	}
	f := flag.NewFlagSet("kubesphere install", flag.ContinueOnError)
	f.SetOutput(diag)
	configPath := f.String("config", defaultConfigPath, "统一配置文件")
	version := f.String("version", "", "KubeSphere 版本；当前内嵌清单仅支持 v3.4.1")
	kubeconfig := f.String("kubeconfig", "", "kubeconfig 路径；默认 KUBECONFIG 或 ~/.kube/config")
	clusterContext := f.String("context", "", "目标 context；默认 kubeconfig 的 current-context，--kind 时推导")
	kindName := f.String("kind", "", "目标 kind 集群名（可选，用于加载安装器镜像）")
	kindBin := f.String("kind-bin", "kind", "kind 可执行文件")
	source := f.String("installer-source", "", "kind 安装器拉取源；清单 image 保持上游名称")
	storageClass := f.String("storage-class", "", "指定现有 StorageClass；默认读取集群默认 SC")
	password := f.String("password", templateDefaults.KubeSphere.Password, "初始 admin 密码；对应 kubesphere.password，不写入密码文件")
	dryRun := f.Bool("dry-run", false, "仅做只读预检；默认执行安装")
	timeout := f.Duration("timeout", 45*time.Minute, "整个安装任务超时，安装器和核心服务就绪等待共用此期限")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	if f.NArg() != 0 || *timeout <= 0 {
		return 2, errors.New("多余参数或 timeout 无效")
	}
	c, err := commandConfig(f, *configPath)
	if err != nil {
		return 2, err
	}
	setEnvFlag(f, "version", "OPSKIT_KUBESPHERE_VERSION")
	setEnvFlag(f, "kind-bin", "OPSKIT_KIND_BIN")
	setConfigFlag(f, "version", c.Versions.KubeSphere)
	if os.Getenv("KUBECONFIG") == "" {
		setConfigFlag(f, "kubeconfig", c.Kind.Kubeconfig)
	}
	setConfigFlag(f, "kind-bin", c.Kind.Binary)
	setConfigFlag(f, "storage-class", c.KubeSphere.StorageClass)
	setConfigFlag(f, "password", c.KubeSphere.Password)
	if err := validateKSPassword(*password); err != nil {
		return 2, err
	}
	if *kindName != "" {
		setConfigFlag(f, "installer-source", c.KubeSphere.InstallerSource)
	}
	*kindBin = configuredKindBinary(c, *kindBin)
	if *version != "v3.4.1" {
		return 2, errors.New("当前已验证的内嵌 KS 清单仅支持 --version v3.4.1；不猜测其他版本地址或改写镜像")
	}
	if *kindName != "" {
		if !clusterNamePattern.MatchString(*kindName) {
			return 2, errors.New("非法 kind 名称")
		}
		if *clusterContext == "" {
			*clusterContext = "kind-" + *kindName
		}
		if *clusterContext != "kind-"+*kindName {
			return 2, errors.New("--kind 与 --context 不一致")
		}
		if *kubeconfig == "" && os.Getenv("KUBECONFIG") == "" {
			home, e := os.UserHomeDir()
			if e != nil {
				return 1, e
			}
			*kubeconfig = filepath.Join(home, ".kube", "kind-"+*kindName+".config")
		}
	}
	if *source != "" && *kindName == "" {
		return 2, errors.New("installer-source 预加载目前仅支持 --kind；自建集群请配置节点 mirror 后使用上游镜像")
	}
	ctx, cancel := context.WithTimeout(parent, *timeout)
	defer cancel()
	base := []string{}
	if *kubeconfig != "" {
		base = append(base, "--kubeconfig", *kubeconfig)
	}
	if *clusterContext == "" {
		raw, e := commandOutput(ctx, nil, "kubectl", append(append([]string{}, base...), "config", "current-context")...)
		if e != nil {
			return 2, fmt.Errorf("无法读取 kubeconfig 当前 context，请检查 KUBECONFIG 或 ~/.kube/config，或指定 --kubeconfig 和 --context: %w", e)
		}
		*clusterContext = strings.TrimSpace(string(raw))
		if *clusterContext == "" {
			return 2, errors.New("kubeconfig 未设置 current-context，请使用 kubectl config use-context 或指定 --context")
		}
	}
	base = append(base, "--context", *clusterContext, "--request-timeout=30s")
	fmt.Fprintf(out, "目标 Kubernetes context: %s\n", *clusterContext)
	kubeOutput := func(a ...string) ([]byte, error) {
		return commandOutput(ctx, nil, "kubectl", append(append([]string{}, base...), a...)...)
	}
	kubeRun := func(input []byte, a ...string) error {
		return commandRun(ctx, input, out, diag, "kubectl", append(append([]string{}, base...), a...)...)
	}
	nodesRaw, err := kubeOutput("get", "nodes", "-o", "json")
	if err != nil {
		return 3, err
	}
	nodes, err := decodeNodes(nodesRaw)
	if err != nil {
		return 1, err
	}
	if len(nodes.Items) == 0 {
		return 2, errors.New("集群没有节点")
	}
	k8sVersion := regexp.MustCompile(`^v1\.(\d+)\.`)
	for _, node := range nodes.Items {
		m := k8sVersion.FindStringSubmatch(node.Status.NodeInfo.KubeletVersion)
		if len(m) != 2 {
			return 2, fmt.Errorf("无法识别节点版本: %s", node.Status.NodeInfo.KubeletVersion)
		}
		minor, _ := strconv.Atoi(m[1])
		if minor < 20 || minor > 26 {
			return 2, fmt.Errorf("KS 3.4.1 需要 K8s 1.20~1.26，节点 %s 为 %s", node.Metadata.Name, node.Status.NodeInfo.KubeletVersion)
		}
		ready := false
		for _, cond := range node.Status.Conditions {
			if cond.Type == "Ready" && cond.Status == "True" {
				ready = true
			}
		}
		if !ready {
			return 2, fmt.Errorf("节点 %s 未 Ready", node.Metadata.Name)
		}
	}
	var classes struct {
		Items []struct {
			Metadata struct {
				Name        string            `json:"name"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		} `json:"items"`
	}
	raw, err := kubeOutput("get", "storageclasses", "-o", "json")
	if err != nil {
		return 3, err
	}
	if err = json.Unmarshal(raw, &classes); err != nil {
		return 1, err
	}
	defaults := []string{}
	exists := false
	for _, sc := range classes.Items {
		if sc.Metadata.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" || sc.Metadata.Annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true" {
			defaults = append(defaults, sc.Metadata.Name)
		}
		if sc.Metadata.Name == *storageClass {
			exists = true
		}
	}
	if len(defaults) != 1 {
		return 2, errors.New("需要唯一默认 StorageClass；先部署并验证 OpenEBS/NFS 等存储驱动")
	}
	if *storageClass == "" {
		*storageClass = defaults[0]
	} else if !exists {
		return 2, errors.New("指定 StorageClass 不存在")
	}
	raw, err = kubeOutput("auth", "can-i", "*", "*", "--all-namespaces")
	if err != nil {
		return 3, err
	}
	if strings.TrimSpace(string(raw)) != "yes" {
		return 2, errors.New("需要 cluster-admin 权限")
	}
	raw, err = kubeOutput("get", "namespace", "kubesphere-system", "--ignore-not-found", "-o", "name")
	if err != nil {
		return 3, err
	}
	if strings.TrimSpace(string(raw)) != "" {
		return 2, errors.New("kubesphere-system 已存在，此入口只做全新安装；不会覆盖已有实例或密码")
	}
	fmt.Fprintf(out, "KS %s → context=%s；StorageClass=%s；节点数=%d\n", *version, *clusterContext, *storageClass, len(nodes.Items))
	if *dryRun {
		fmt.Fprintln(out, "预检通过；去掉 --dry-run 执行安装。存储驱动和节点 mirror 不会自动安装。")
		return 0, nil
	}
	configuration, err := renderKSConfig(templates.KSConfiguration, *storageClass, *password)
	if err != nil {
		return 1, err
	}
	if *kindName != "" {
		image := "docker.io/kubesphere/ks-installer:v3.4.1"
		pull := image
		if *source != "" {
			pull = *source
		}
		if _, e := commandOutput(ctx, nil, "docker", "image", "inspect", pull); e != nil {
			if e = commandRun(ctx, nil, out, diag, "docker", "pull", pull); e != nil {
				return 1, e
			}
		}
		if pull != image {
			if err = commandRun(ctx, nil, out, diag, "docker", "tag", pull, image); err != nil {
				return 1, err
			}
		}
		if err = commandRun(ctx, nil, out, diag, *kindBin, "load", "docker-image", image, "--name", *kindName); err != nil {
			return 1, err
		}
	}
	if err = kubeRun(templates.KSInstaller, "apply", "-f", "-"); err != nil {
		return 1, err
	}
	if err = kubeRun(nil, "wait", "--for=condition=Established", "crd/clusterconfigurations.installer.kubesphere.io", "--timeout=60s"); err != nil {
		return 1, err
	}
	if err = kubeRun(configuration, "apply", "-f", "-"); err != nil {
		return 1, err
	}
	if err = kubeRun(nil, "-n", "kubesphere-system", "rollout", "status", "deployment/ks-installer", "--timeout="+timeout.String()); err != nil {
		return 1, err
	}
	fmt.Fprintln(out, "ks-installer 已就绪；最近 100 行安装日志：")
	if err = kubeRun(nil, "-n", "kubesphere-system", "logs", "deployment/ks-installer", "--tail=100"); err != nil {
		return 1, err
	}
	fmt.Fprintln(out, "等待 KubeSphere core 状态变为 enabled…")
	if err = waitKubeSphereCore(ctx, 5*time.Second, func(ctx context.Context) ([]byte, error) {
		return commandOutput(ctx, nil, "kubectl", append(append([]string{}, base...), "-n", "kubesphere-system", "get", "clusterconfiguration/ks-installer", "-o", "json")...)
	}); err != nil {
		return 1, err
	}
	progressSeconds := int64((*timeout + time.Second - 1) / time.Second)
	progressPatch := fmt.Sprintf(`{"spec":{"progressDeadlineSeconds":%d}}`, progressSeconds)
	components := []string{"ks-apiserver", "ks-controller-manager", "ks-console"}
	for _, component := range components {
		if err = kubeRun(nil, "-n", "kubesphere-system", "patch", "deployment/"+component, "--type=merge", "-p", progressPatch); err != nil {
			return 1, err
		}
	}
	for _, component := range components {
		if err = kubeRun(nil, "-n", "kubesphere-system", "rollout", "status", "deployment/"+component, "--timeout="+timeout.String()); err != nil {
			return 1, err
		}
	}
	fmt.Fprintf(out, "KS %s 核心服务已就绪；控制台端口 30880\nAccount: admin\nPassword: %s\n", *version, *password)
	return 0, kubeRun(nil, "-n", "kubesphere-system", "get", "service", "ks-console")
}
func renderKSConfig(template []byte, storage, password string) ([]byte, error) {
	var data map[string]any
	if err := yaml.Unmarshal(template, &data); err != nil {
		return nil, err
	}
	spec, ok := data["spec"].(map[string]any)
	if !ok {
		return nil, errors.New("KS 模板缺少 spec")
	}
	persistence, ok := spec["persistence"].(map[string]any)
	if !ok {
		return nil, errors.New("KS 模板缺少 persistence")
	}
	persistence["storageClass"] = storage
	auth, ok := spec["authentication"].(map[string]any)
	if !ok {
		return nil, errors.New("KS 模板缺少 authentication")
	}
	auth["adminPassword"] = password
	return yaml.Marshal(data)
}

func waitKubeSphereCore(ctx context.Context, interval time.Duration, read func(context.Context) ([]byte, error)) error {
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("等待 KubeSphere core enabled: %w", err)
		}
		raw, err := read(ctx)
		if err != nil {
			return fmt.Errorf("读取 KubeSphere 安装状态: %w", err)
		}
		var resource struct {
			Status struct {
				Core struct {
					Status string `json:"status"`
				} `json:"core"`
			} `json:"status"`
		}
		if err := json.Unmarshal(raw, &resource); err != nil {
			return fmt.Errorf("解析 KubeSphere 安装状态: %w", err)
		}
		if resource.Status.Core.Status == "enabled" {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("等待 KubeSphere core enabled: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func validateKSPassword(password string) error {
	if len(password) < 8 || len(password) > 64 || !regexp.MustCompile(`[a-z]`).MatchString(password) || !regexp.MustCompile(`[A-Z]`).MatchString(password) || !regexp.MustCompile(`[0-9]`).MatchString(password) {
		return errors.New("kubesphere.password 必须 8~64 字节，并包含大写字母、小写字母和数字")
	}
	return nil
}
