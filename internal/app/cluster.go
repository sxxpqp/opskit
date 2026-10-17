package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func explicitFlag(f *flag.FlagSet, name string) bool {
	found := false
	f.Visit(func(v *flag.Flag) {
		if v.Name == name {
			found = true
		}
	})
	return found
}
func setConfigFlag(f *flag.FlagSet, name, value string) {
	if value != "" && !explicitFlag(f, name) && f.Lookup(name) != nil {
		_ = f.Set(name, value)
	}
}
func setEnvFlag(f *flag.FlagSet, name, env string) {
	if value := os.Getenv(env); value != "" && !explicitFlag(f, name) && f.Lookup(name) != nil {
		_ = f.Set(name, value)
	}
}
func commandConfig(f *flag.FlagSet, path string) (fileConfig, error) {
	c, err := readFileConfig(path, explicitFlag(f, "config"))
	if err != nil {
		return c, err
	}
	if path == defaultConfigPath && !explicitFlag(f, "config") {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			path = "opskit.yaml"
		}
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return c, err
	}
	resolve := func(s string) string {
		if s != "" && !filepath.IsAbs(s) {
			return filepath.Join(base, s)
		}
		return s
	}
	c.Install.DownloadDir = resolve(c.Install.DownloadDir)
	c.Kind.Kubeconfig = resolve(c.Kind.Kubeconfig)
	if strings.ContainsAny(c.Kind.Binary, "/\\") {
		c.Kind.Binary = resolve(c.Kind.Binary)
	}
	return c, nil
}

// Commands are executed as argument arrays, never interpolated into a host shell.
func commandOutput(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "KIND_EXPERIMENTAL_PROVIDER=docker")
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	var output, diag bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &diag
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(diag.String()))
	}
	return output.Bytes(), nil
}
func commandRun(ctx context.Context, input []byte, out, diag io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "KIND_EXPERIMENTAL_PROVIDER=docker")
	cmd.Stdout = out
	cmd.Stderr = diag
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

var clusterNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)

type clusterOptions struct {
	name, kubeconfig, binary, nodeVersion string
	workers, port, httpPort, httpsPort    int
}

func clusterFlags(f *flag.FlagSet) *clusterOptions {
	home, _ := os.UserHomeDir()
	o := &clusterOptions{}
	f.StringVar(&o.name, "name", "kubesphere-341", "kind 集群名")
	f.StringVar(&o.kubeconfig, "kubeconfig", filepath.Join(home, ".kube", "kind-kubesphere-341.config"), "kubeconfig 文件")
	f.StringVar(&o.binary, "kind-bin", "kind", "kind 可执行文件")
	f.StringVar(&o.nodeVersion, "version", "v1.24.7", "创建节点的 Kubernetes 版本")
	f.IntVar(&o.workers, "workers", 2, "worker 数量")
	f.IntVar(&o.port, "console-port", 30880, "0.0.0.0 上的 KS 控制台映射端口")
	f.IntVar(&o.httpPort, "http-port", 80, "Ingress HTTP 宿主机端口，映射控制平面节点 80，监听 0.0.0.0")
	f.IntVar(&o.httpsPort, "https-port", 443, "Ingress HTTPS 宿主机端口，映射控制平面节点 443，监听 0.0.0.0")
	return o
}
func applyClusterConfig(f *flag.FlagSet, o *clusterOptions, c fileConfig) error {
	setEnvFlag(f, "name", "OPSKIT_KIND_NAME")
	setEnvFlag(f, "kubeconfig", "KUBECONFIG")
	setEnvFlag(f, "kind-bin", "OPSKIT_KIND_BIN")
	setEnvFlag(f, "version", "OPSKIT_KUBERNETES_VERSION")
	setConfigFlag(f, "name", c.Kind.Name)
	setConfigFlag(f, "kubeconfig", c.Kind.Kubeconfig)
	setConfigFlag(f, "kind-bin", c.Kind.Binary)
	setConfigFlag(f, "version", c.Versions.Kubernetes)
	if c.Kind.Workers != nil {
		setConfigFlag(f, "workers", strconv.Itoa(*c.Kind.Workers))
	}
	if c.Kind.ConsolePort != 0 {
		setConfigFlag(f, "console-port", strconv.Itoa(c.Kind.ConsolePort))
	}
	if c.Kind.HTTPPort != 0 {
		setConfigFlag(f, "http-port", strconv.Itoa(c.Kind.HTTPPort))
	}
	if c.Kind.HTTPSPort != 0 {
		setConfigFlag(f, "https-port", strconv.Itoa(c.Kind.HTTPSPort))
	}
	if !explicitFlag(f, "kubeconfig") {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		o.kubeconfig = filepath.Join(home, ".kube", "kind-"+o.name+".config")
	}
	o.binary = configuredKindBinary(c, o.binary)
	if !clusterNamePattern.MatchString(o.name) || o.workers < 0 || o.workers > 100 || o.port < 1 || o.port > 65535 {
		return errors.New("集群名、workers 或 console-port 无效")
	}
	if o.httpPort < 1 || o.httpPort > 65535 || o.httpsPort < 1 || o.httpsPort > 65535 || o.httpPort == o.httpsPort || o.httpPort == o.port || o.httpsPort == o.port {
		return errors.New("http-port、https-port 须在 1～65535，且不能互相或与 console-port 重复")
	}
	if !versionPattern.MatchString(o.nodeVersion) {
		return errors.New("Kubernetes 版本须为 vX.Y.Z")
	}
	return nil
}
func configuredKindBinary(c fileConfig, binary string) string {
	if binary != "kind" {
		return binary
	}
	home, err := os.UserHomeDir()
	if err != nil || !versionPattern.MatchString(c.Versions.Kind) {
		return binary
	}
	name := "kind"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(home, ".local", "share", "opskit", "kind", c.Versions.Kind, name)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return binary
}

func (o clusterOptions) kubeArgs(args ...string) []string {
	return append([]string{"--kubeconfig", o.kubeconfig, "--context", "kind-" + o.name, "--request-timeout=30s"}, args...)
}
func renderKind(o clusterOptions) ([]byte, error) {
	ports := []any{
		map[string]any{"containerPort": 80, "hostPort": o.httpPort, "listenAddress": "0.0.0.0", "protocol": "TCP"},
		map[string]any{"containerPort": 443, "hostPort": o.httpsPort, "listenAddress": "0.0.0.0", "protocol": "TCP"},
		map[string]any{"containerPort": 30880, "hostPort": o.port, "listenAddress": "0.0.0.0", "protocol": "TCP"},
	}
	nodes := []any{map[string]any{"role": "control-plane", "image": "kindest/node:" + o.nodeVersion, "labels": map[string]string{"ingress-ready": "true"}, "extraPortMappings": ports}}

	for i := 0; i < o.workers; i++ {
		nodes = append(nodes, map[string]any{"role": "worker", "image": "kindest/node:" + o.nodeVersion})
	}
	return yaml.Marshal(map[string]any{"kind": "Cluster", "apiVersion": "kind.x-k8s.io/v1alpha4", "nodes": nodes})
}
func runKind(parent context.Context, args []string, out, diag io.Writer) (int, error) {
	if len(args) == 0 {
		return 2, errors.New("kind create|status|logs|delete|use|unuse")
	}
	action := args[0]
	switch action {
	case "create", "status", "logs", "delete", "use", "unuse":
	default:
		return 2, fmt.Errorf("未知 kind 命令: %s", action)
	}
	f := flag.NewFlagSet("kind "+action, flag.ContinueOnError)
	f.SetOutput(diag)
	path := f.String("config", defaultConfigPath, "统一配置文件")
	o := clusterFlags(f)
	var dryRun bool
	if action == "delete" {
		f.BoolVar(&dryRun, "dry-run", false, "仅预览删除，不执行")
	}
	if action == "create" {
		f.BoolVar(&dryRun, "dry-run", false, "仅预览集群配置，不创建集群")
	}
	logDir := f.String("directory", "", "导出诊断日志的目录")
	timeout := f.Duration("timeout", 10*time.Minute, "任务超时")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	if f.NArg() != 0 || *timeout <= 0 {
		return 2, errors.New("多余参数或 timeout 无效")
	}
	c, err := commandConfig(f, *path)
	if err != nil {
		return 2, err
	}
	if err = applyClusterConfig(f, o, c); err != nil {
		return 2, err
	}
	ctx, cancel := context.WithTimeout(parent, *timeout)
	defer cancel()
	switch action {
	case "create":
		config, err := renderKind(*o)
		if err != nil {
			return 1, err
		}
		fmt.Fprintf(out, "kind create: %s; Kubernetes %s; workers=%d; kubeconfig=%s\n", o.name, o.nodeVersion, o.workers, o.kubeconfig)
		fmt.Fprintf(out, "Ingress 端口: 0.0.0.0:%d → control-plane:80，0.0.0.0:%d → control-plane:443；需单独部署 Ingress Controller。\n", o.httpPort, o.httpsPort)
		fmt.Fprintf(out, "KS 控制台端口: 0.0.0.0:%d → control-plane:30880\n", o.port)
		if dryRun {
			if _, err = out.Write(config); err != nil {
				return 1, err
			}
			fmt.Fprintln(out, "仅预览：未创建集群；去掉 --dry-run 执行创建。")
			return 0, nil
		}
		if _, err = exec.LookPath("kubectl"); err != nil {
			return 3, fmt.Errorf("kubectl dependency: %w", err)
		}
		if _, err = commandOutput(ctx, nil, "docker", "info"); err != nil {
			return 3, err
		}
		if c.Versions.Kind != "" {
			if err = verifyKind(ctx, o.binary, c.Versions.Kind); err != nil {
				return 3, err
			}
		}
		current, err := commandOutput(ctx, nil, o.binary, "get", "clusters")
		if err != nil {
			return 3, err
		}
		for _, name := range strings.Fields(string(current)) {
			if name == o.name {
				return 1, fmt.Errorf("集群 %s 已存在，保留现有拓扑；请使用 kind status", name)
			}
		}
		if err = os.MkdirAll(filepath.Dir(o.kubeconfig), 0700); err != nil {
			return 1, err
		}
		if err = commandRun(ctx, config, out, diag, o.binary, "create", "cluster", "--name", o.name, "--config", "-", "--kubeconfig", o.kubeconfig, "--retain", "--wait", "180s"); err != nil {
			return 1, err
		}
		return 0, commandRun(ctx, nil, out, diag, "kubectl", o.kubeArgs("wait", "--for=condition=Ready", "nodes", "--all", "--timeout=180s")...)
	case "status":
		for _, a := range [][]string{{"get", "nodes", "-o", "wide"}, {"get", "pods", "-A"}, {"get", "sc"}, {"get", "pvc", "-A"}} {
			if err = commandRun(ctx, nil, out, diag, "kubectl", o.kubeArgs(a...)...); err != nil {
				return 1, err
			}
		}
	case "logs":
		if *logDir == "" {
			*logDir, err = os.MkdirTemp("", "opskit-kind-logs-")
			if err != nil {
				return 1, err
			}
		}
		err = commandRun(ctx, nil, out, diag, o.binary, "export", "logs", *logDir, "--name", o.name)
		if err == nil {
			fmt.Fprintln(out, "日志目录:", *logDir)
		}
	case "delete":
		if dryRun {
			fmt.Fprintf(out, "预览: 删除 kind 集群 %s；去掉 --dry-run 执行删除\n", o.name)
			return 0, nil
		}
		fmt.Fprintf(out, "删除 kind 集群: %s\n", o.name)
		err = commandRun(ctx, nil, out, diag, o.binary, "delete", "cluster", "--name", o.name, "--kubeconfig", o.kubeconfig)
	case "use", "unuse":
		err = switchKindContext(ctx, *o, action, out)
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}
func switchKindContext(ctx context.Context, o clusterOptions, action string, out io.Writer) error {
	// Work in a separate merged file, so merge failure cannot truncate the user's kubeconfig.
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	target := os.Getenv("OPSKIT_USER_KUBECONFIG")
	if target == "" {
		target = filepath.Join(home, ".kube", "config")
	}
	state := o.kubeconfig + ".previous-context"
	if action == "unuse" {
		previous, err := os.ReadFile(state)
		if err != nil {
			return err
		}
		args := []string{"--kubeconfig", target, "config", "use-context", string(previous)}
		if len(previous) == 0 {
			args = []string{"--kubeconfig", target, "config", "unset", "current-context"}
		}
		if err := commandRun(ctx, nil, out, out, "kubectl", args...); err != nil {
			return err
		}
		return os.Remove(state)
	}
	if filepath.Clean(target) == filepath.Clean(o.kubeconfig) {
		return errors.New("用户 kubeconfig 与 kind kubeconfig 不能为同一文件")
	}
	var previous []byte
	if _, err = os.Stat(state); os.IsNotExist(err) {
		if _, e := os.Stat(target); e == nil {
			previous, err = commandOutput(ctx, nil, "kubectl", "--kubeconfig", target, "config", "view", "-o", "jsonpath={.current-context}")
			if err != nil {
				return err
			}
		}
		if err = writeConfigFile(state, previous, out); err != nil {
			return err
		}
	}
	cmd := exec.CommandContext(ctx, "kubectl", "config", "view", "--raw", "--flatten")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+o.kubeconfig+string(os.PathListSeparator)+target)
	data, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("合并 kubeconfig: %w", err)
	}
	if err = writeConfigFile(target, data, out); err != nil {
		return err
	}
	return commandRun(ctx, nil, out, out, "kubectl", "--kubeconfig", target, "config", "use-context", "kind-"+o.name)
}

// Generic JSON node shape used for compatibility/readiness checks.
type clusterNodes struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			NodeInfo struct {
				KubeletVersion string `json:"kubeletVersion"`
			} `json:"nodeInfo"`
			Conditions []struct{ Type, Status string } `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

func decodeNodes(data []byte) (clusterNodes, error) {
	var n clusterNodes
	err := json.Unmarshal(data, &n)
	return n, err
}
