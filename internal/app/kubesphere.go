package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	templates "github.com/sxxpqp/opskit"
	"gopkg.in/yaml.v3"
)

func runKubeSphere(parent context.Context, args []string, out, diag io.Writer) (int, error) {
	f := flag.NewFlagSet("kubesphere install", flag.ContinueOnError)
	f.SetOutput(diag)
	configPath := f.String("config", defaultConfigPath, "统一配置文件")
	version := f.String("version", "", "KubeSphere 版本；当前内嵌清单仅支持 v3.4.1")
	kubeconfig := f.String("kubeconfig", "", "kubeconfig 路径")
	clusterContext := f.String("context", "", "目标 context；必须显式指定或通过 --kind 推导")
	kindName := f.String("kind", "", "目标 kind 集群名（可选，用于加载安装器镜像）")
	kindBin := f.String("kind-bin", "kind", "kind 可执行文件")
	source := f.String("installer-source", "", "kind 安装器拉取源；清单 image 保持上游名称")
	storageClass := f.String("storage-class", "", "指定现有 StorageClass；默认读取集群默认 SC")
	passwordPath := f.String("password-file", "", "初始密码文件，默认在 ~/.kube 中生成")
	apply := f.Bool("apply", false, "执行安装；默认只读预检")
	timeout := f.Duration("timeout", 20*time.Minute, "整个安装任务超时")
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
	setEnvFlag(f, "kubeconfig", "KUBECONFIG")
	setEnvFlag(f, "kind-bin", "OPSKIT_KIND_BIN")
	setConfigFlag(f, "version", c.Versions.KubeSphere)
	setConfigFlag(f, "kubeconfig", c.Kind.Kubeconfig)
	setConfigFlag(f, "kind-bin", c.Kind.Binary)
	setConfigFlag(f, "storage-class", c.KubeSphere.StorageClass)
	setConfigFlag(f, "password-file", c.KubeSphere.PasswordFile)
	setConfigFlag(f, "installer-source", c.KubeSphere.InstallerSource)
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
		if *kubeconfig == "" {
			home, e := os.UserHomeDir()
			if e != nil {
				return 1, e
			}
			*kubeconfig = filepath.Join(home, ".kube", "kind-"+*kindName+".config")
		}
	}
	if *clusterContext == "" || *kubeconfig == "" {
		return 2, errors.New("必须指定 --kubeconfig 和 --context，或通过 --kind 与配置提供")
	}
	if *source != "" && *kindName == "" {
		return 2, errors.New("installer-source 预加载目前仅支持 --kind；自建集群请配置节点 mirror 后使用上游镜像")
	}
	ctx, cancel := context.WithTimeout(parent, *timeout)
	defer cancel()
	base := []string{"--kubeconfig", *kubeconfig, "--context", *clusterContext, "--request-timeout=30s"}
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
	if !*apply {
		fmt.Fprintln(out, "预检通过；--apply 执行安装。存储驱动和节点 mirror 不会自动安装。")
		return 0, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return 1, err
	}
	if *passwordPath == "" {
		*passwordPath = filepath.Join(home, ".kube", "ks341-"+regexp.MustCompile(`[^a-zA-Z0-9_.-]`).ReplaceAllString(*clusterContext, "_")+".secret")
	}
	password, err := initialPassword(*passwordPath)
	if err != nil {
		return 1, err
	}
	configuration, err := renderKSConfig(templates.KSConfiguration, *storageClass, password)
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
	if err = kubeRun(nil, "-n", "kubesphere-system", "wait", "--for=jsonpath={.status.core.status}=enabled", "clusterconfiguration/ks-installer", "--timeout=900s"); err != nil {
		return 1, err
	}
	for _, component := range []string{"ks-apiserver", "ks-controller-manager", "ks-console"} {
		if err = kubeRun(nil, "-n", "kubesphere-system", "rollout", "status", "deployment/"+component, "--timeout=180s"); err != nil {
			return 1, err
		}
	}
	fmt.Fprintf(out, "KS %s 核心服务已就绪；admin 密码文件: %s；控制台端口 30880\n", *version, *passwordPath)
	return 0, kubeRun(nil, "-n", "kubesphere-system", "get", "service", "ks-console")
}
func initialPassword(path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return "", errors.New("密码文件必须是普通文件")
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			return "", errors.New("密码文件必须为 0600 权限")
		}
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		random := make([]byte, 16)
		if _, err = rand.Read(random); err != nil {
			return "", err
		}
		file, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return "", e
		}
		_, err = file.WriteString("Ks341-7" + hex.EncodeToString(random) + "\n")
		closeErr := file.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	password := strings.TrimSuffix(string(raw), "\n")
	if len(password) < 8 || len(password) > 64 || !regexp.MustCompile(`[A-Z]`).MatchString(password) || !regexp.MustCompile(`[a-z]`).MatchString(password) || !regexp.MustCompile(`[0-9]`).MatchString(password) {
		return "", errors.New("密码必须 8~64 字符并包含大小写字母和数字")
	}
	return password, nil
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
