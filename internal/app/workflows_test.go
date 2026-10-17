package app

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	templates "github.com/sxxpqp/opskit"
	"gopkg.in/yaml.v3"
)

func fakeWorkflowTool() {
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	args := strings.Join(os.Args[1:], " ")
	dir := os.Getenv("OPSKIT_FAKE_DIR")
	file, e := os.OpenFile(filepath.Join(dir, "events"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		os.Exit(90)
	}
	fmt.Fprintln(file, name+" "+args)
	file.Close()
	if name == "kind" {
		switch {
		case args == "version":
			fmt.Print("kind v0.17.0 mock\n")
		case strings.Contains(args, "get clusters"):
			fmt.Print(os.Getenv("OPSKIT_FAKE_EXISTING"))
		case strings.Contains(args, "get nodes"):
			fmt.Print("control-plane\nworker\n")
		case strings.Contains(args, "create cluster"):
			data, _ := io.ReadAll(os.Stdin)
			_ = os.WriteFile(filepath.Join(dir, "kind.yaml"), data, 0600)
		}
	}
	if name == "docker" {
		switch {
		case strings.Contains(args, "cat /etc/containerd/config.toml"):
			if strings.Contains(args, os.Getenv("OPSKIT_FAKE_FAIL_NODE")) && os.Getenv("OPSKIT_FAKE_FAIL_NODE") != "" {
				os.Exit(8)
			}
			fmt.Print("version = 2\n[plugins.\"io.containerd.grpc.v1.cri\".registry]\n config_path = \"\"\n[plugins.\"io.containerd.grpc.v1.cri\".registry.mirrors]\n")
		case strings.Contains(args, "mktemp -d"):
			fmt.Print("/etc/containerd/opskit-backup.fixture\n")
		}
	}
	if name == "kubectl" {
		switch {
		case args == "version --client=true --output=json":
			if os.Getenv("OPSKIT_FAKE_KUBECTL_JSON") == "invalid" {
				fmt.Print("not-json")
			} else {
				fmt.Printf(`{"clientVersion":{"gitVersion":%q}}`, envOr("OPSKIT_FAKE_KUBECTL_VERSION", "v1.24.7"))
			}
		case strings.HasSuffix(args, "config current-context"):
			fmt.Print(os.Getenv("OPSKIT_FAKE_CURRENT_CONTEXT"))
		case strings.Contains(args, "config view -o jsonpath="):
			fmt.Print("original-context")
		case args == "config view --raw --flatten":
			fmt.Print("apiVersion: v1\nkind: Config\nusers:\n- name: original\n  user:\n    token: EXISTING-TOKEN\ncontexts:\n- name: original-context\n- name: kind-test\n")
		case strings.Contains(args, "get clusterconfiguration/ks-installer -o json"):
			fmt.Print(`{"status":{"core":{"status":"enabled"}}}`)
		case strings.Contains(args, "get nodes -o json"):
			fmt.Printf(`{"items":[{"metadata":{"name":"node1"},"status":{"nodeInfo":{"kubeletVersion":%q},"conditions":[{"type":"Ready","status":"True"}]}}]}`, envOr("OPSKIT_FAKE_K8S", "v1.24.7"))
		case strings.Contains(args, "get storageclasses"):
			fmt.Print(`{"items":[{"metadata":{"name":"openebs-hostpath","annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}]}`)
		case strings.Contains(args, "auth can-i"):
			fmt.Print("yes\n")
		case strings.Contains(args, "get namespace"):
			if os.Getenv("OPSKIT_FAKE_KS_EXISTS") != "" {
				fmt.Print("namespace/kubesphere-system")
			}
		case strings.Contains(args, "apply -f -"):
			data, _ := io.ReadAll(os.Stdin)
			p := filepath.Join(dir, "applied.yaml")
			f, _ := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			f.Write(data)
			f.WriteString("\n---\n")
			f.Close()
		}
	}
	os.Exit(0)
}
func envOr(name, value string) string {
	if s := os.Getenv(name); s != "" {
		return s
	}
	return value
}
func fakeTools(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"kind", "docker", "kubectl"} {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		path := filepath.Join(dir, name)
		// Windows cannot delete hard links to the running test executable.
		// Give each fake tool its own file so TempDir cleanup can remove it.
		linked := false
		if runtime.GOOS != "windows" {
			linked = os.Link(exe, path) == nil
		}
		if !linked {
			data, err := os.ReadFile(exe)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, data, 0755); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv("OPSKIT_FAKE_TOOLS", "1")
	t.Setenv("OPSKIT_FAKE_DIR", dir)
	return dir
}
func TestFakeToolsCanBeRemoved(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows executable sharing regression")
	}
	dir := fakeTools(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"kind.exe", "docker.exe", "kubectl.exe"} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(original, info) {
			t.Fatalf("%s shares the running test executable", name)
		}
		if err := os.Remove(path); err != nil {
			t.Fatalf("remove %s while test executable is running: %v", name, err)
		}
	}
}

func workflowRun(t *testing.T, args ...string) (int, error, string) {
	t.Helper()
	var output bytes.Buffer
	code, err := Run(context.Background(), args, &output, &output, "test")
	return code, err, output.String()
}
func TestCreateDryRunDefaultExecutionAndExisting(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := fakeTools(t)
	config := filepath.Join(dir, "kube.config")
	args := []string{"kind", "create", "--name", "test", "--version", "v1.24.7", "--workers", "2", "--kubeconfig", config}
	code, err, text := workflowRun(t, append(args, "--dry-run")...)
	if code != 0 || err != nil {
		t.Fatal(code, err)
	}
	if _, e := os.Stat(filepath.Join(dir, "events")); !os.IsNotExist(e) {
		t.Fatal("preview executed commands")
	}
	if !strings.Contains(text, "kindest/node:v1.24.7") || !strings.Contains(text, "未创建集群") {
		t.Fatal(text)
	}
	code, err, _ = workflowRun(t, append(args, "--apply")...)
	if code == 0 || err == nil {
		t.Fatal("removed --apply flag accepted")
	}
	if _, e := os.Stat(filepath.Join(dir, "events")); !os.IsNotExist(e) {
		t.Fatal("invalid flags executed commands")
	}
	code, err, text = workflowRun(t, args...)
	if code != 0 || err != nil {
		t.Fatal(code, err, text)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "kind.yaml"))
	var cluster map[string]any
	if e := yaml.Unmarshal(raw, &cluster); e != nil {
		t.Fatal(e)
	}
	if len(cluster["nodes"].([]any)) != 3 {
		t.Fatal("wrong node count")
	}
	t.Setenv("OPSKIT_FAKE_EXISTING", "test\n")
	code, err, _ = workflowRun(t, args...)
	if code == 0 || err == nil {
		t.Fatal("existing cluster overwritten")
	}
}
func TestDeleteRejectsRemovedConfirmation(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := fakeTools(t)
	code, err, _ := workflowRun(t, "kind", "delete", "--name", "test", "--confirm-name", "wrong")
	if code == 0 || err == nil {
		t.Fatal("removed confirm-name accepted")
	}
	if _, e := os.Stat(filepath.Join(dir, "events")); !os.IsNotExist(e) {
		t.Fatal("delete command executed")
	}
}
func TestMirrorPreservesOtherSettings(t *testing.T) {
	for _, tool := range []string{"go", "npm", "pip", "maven"} {
		text, err := renderLanguage(tool, nil, "https://mirror.example.com/repository")
		if err != nil {
			t.Fatal(tool, err)
		}
		again, err := renderLanguage(tool, text, "https://mirror.example.com/repository")
		if err != nil || string(text) != string(again) {
			t.Fatal(tool, "not idempotent", err)
		}
	}
	if _, err := renderLanguage("npm", nil, "https://host/\nunsafe=true"); err == nil {
		t.Fatal("newline accepted")
	}
	if _, err := renderLanguage("maven", []byte(`<settings><mirrors><mirror><id>custom</id></mirror></mirrors></settings>`), "https://mirror.example.com"); err == nil {
		t.Fatal("custom mirrors overwritten")
	}
	text := replaceSetting("[global]\ntimeout=60\nindex-url=old\n[install]\ntrusted-host=host\n", "global", "index-url", "https://new.example.com")
	if !strings.Contains(text, "timeout=60") || !strings.Contains(text, "[install]\ntrusted-host=host") || strings.Contains(text, "index-url=old") {
		t.Fatal(text)
	}
}
func TestContainerdCompatibilityAndTraversal(t *testing.T) {
	for _, plugin := range []string{`io.containerd.grpc.v1.cri`, `io.containerd.cri.v1.images`} {
		text := `[plugins."` + plugin + `".registry]` + "\n config_path = \"\"\n" + `[plugins."` + plugin + `".registry.mirrors]` + "\n" + `[plugins."` + plugin + `".registry.mirrors."docker.io"]` + "\nendpoint=[\"old\"]\n[other]\nkeep=true\n"
		if _, err := configureRegistry(text, false); err == nil {
			t.Fatal("host legacy silently replaced")
		}
		converted, err := configureRegistry(text, true)
		if err != nil || strings.Contains(converted, "endpoint=") || !strings.Contains(converted, "keep=true") {
			t.Fatal(converted, err)
		}
		again, err := configureRegistry(converted, true)
		if err != nil || again != converted {
			t.Fatal("migration not idempotent")
		}
	}
	if _, err := renderHosts(map[string]registryMirror{"../escape": {Server: "https://docker.io"}}); err == nil {
		t.Fatal("registry traversal allowed")
	}
}
func TestKindMirrorPreflightBeforeWrites(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := fakeTools(t)
	config := filepath.Join(dir, "config.yaml")
	content := "schema_version: 1\nmirrors:\n containerd:\n  docker.io:\n   server: https://registry-1.docker.io\n   endpoint: https://mirror.example.com/v2/dockerhub\n"
	if err := os.WriteFile(config, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"mirror", "containerd", "--kind", "test", "--config", config}
	t.Setenv("OPSKIT_FAKE_FAIL_NODE", "worker")
	code, err, _ := workflowRun(t, args...)
	if code == 0 || err == nil {
		t.Fatal("preflight failure ignored")
	}
	events, _ := os.ReadFile(filepath.Join(dir, "events"))
	if strings.Contains(string(events), "mktemp") || strings.Contains(string(events), "docker cp") {
		t.Fatal("modified nodes before all checks passed")
	}
	t.Setenv("OPSKIT_FAKE_FAIL_NODE", "")
	code, err, text := workflowRun(t, args...)
	if code != 0 || err != nil {
		t.Fatal(code, err, text)
	}
	events, _ = os.ReadFile(filepath.Join(dir, "events"))
	if strings.Count(string(events), "systemctl --no-pager restart containerd") != 2 {
		t.Fatal("not all nodes configured")
	}
}
func TestKubeSpherePreflightDefaultInstallAndProtection(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := fakeTools(t)
	config := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(config, []byte("schema_version: 1\nkubesphere:\n  password: ConfigPassword123!\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"kubesphere", "install", "--version", "v3.4.1", "--kind", "test", "--config", config, "--timeout", "45m"}
	code, err, text := workflowRun(t, append(args, "--dry-run")...)
	if code != 0 || err != nil {
		t.Fatal(code, err, text)
	}
	if _, e := os.Stat(filepath.Join(dir, "applied.yaml")); !os.IsNotExist(e) {
		t.Fatal("preview applied configuration")
	}
	t.Setenv("OPSKIT_FAKE_K8S", "v1.35.0")
	code, err, _ = workflowRun(t, args...)
	if code == 0 || err == nil {
		t.Fatal("unsupported K8s installed")
	}
	t.Setenv("OPSKIT_FAKE_K8S", "v1.24.7")
	t.Setenv("OPSKIT_FAKE_KS_EXISTS", "yes")
	code, err, _ = workflowRun(t, args...)
	if code == 0 || err == nil {
		t.Fatal("existing KS overwritten")
	}
	t.Setenv("OPSKIT_FAKE_KS_EXISTS", "")
	code, err, text = workflowRun(t, args...)
	if code != 0 || err != nil {
		t.Fatal(code, err, text)
	}
	if strings.Contains(text, "ConfigPassword123!") {
		t.Fatal("password leaked")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "applied.yaml"))
	if !strings.Contains(string(raw), "openebs-hostpath") || !strings.Contains(string(raw), "adminPassword: ConfigPassword123!") {
		t.Fatal("runtime configuration not injected")
	}
	events, _ := os.ReadFile(filepath.Join(dir, "events"))
	for _, component := range []string{"ks-installer", "ks-apiserver", "ks-controller-manager", "ks-console"} {
		if !strings.Contains(string(events), "rollout status deployment/"+component+" --timeout=45m0s") {
			t.Fatal("rollout timeout did not follow task timeout", string(events))
		}
	}
	for _, component := range []string{"ks-apiserver", "ks-controller-manager", "ks-console"} {
		patch := strings.Index(string(events), "patch deployment/"+component+` --type=merge -p {"spec":{"progressDeadlineSeconds":2700}}`)
		rollout := strings.Index(string(events), "rollout status deployment/"+component)
		if patch < 0 || rollout < patch {
			t.Fatal("progress deadline must be extended before rollout", string(events))
		}
	}
	rollout := strings.Index(string(events), "rollout status deployment/ks-installer")
	logs := strings.Index(string(events), "logs deployment/ks-installer --tail=100")
	core := strings.Index(string(events), "get clusterconfiguration/ks-installer -o json")
	if rollout < 0 || logs < rollout || core < logs {
		t.Fatal("expected installer rollout, logs, then core wait", string(events))
	}
	if strings.Index(string(events), "kind load docker-image") > strings.Index(string(events), "apply -f -") {
		t.Fatal("installer loaded after apply")
	}
}
func TestDefaultConfigAndLegacyFallback(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("opskit.yaml", []byte("schema_version: 1\nversions:\n kind: v0.17.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := readFileConfig(defaultConfigPath, false)
	if err != nil || c.Versions.Kind != "v0.17.0" {
		t.Fatal(c, err)
	}
	if err = os.MkdirAll("config", 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(defaultConfigPath, templates.ConfigTemplate, 0600); err != nil {
		t.Fatal(err)
	}
	c, err = readFileConfig(defaultConfigPath, false)
	if err != nil || c.Versions.KubeSphere != "v3.4.1" {
		t.Fatal(c, err)
	}
	if _, err = readFileConfig("missing.yaml", true); err == nil {
		t.Fatal("explicit missing file ignored")
	}
}

func TestClusterConfigPrecedence(t *testing.T) {
	f := flag.NewFlagSet("test", flag.ContinueOnError)
	o := clusterFlags(f)
	if err := f.Parse([]string{"--workers", "3"}); err != nil {
		t.Fatal(err)
	}
	var c fileConfig
	c.Kind.Name = "yaml-cluster"
	workers := 1
	c.Kind.Workers = &workers
	t.Setenv("OPSKIT_KIND_NAME", "env-cluster")
	if err := applyClusterConfig(f, o, c); err != nil {
		t.Fatal(err)
	}
	if o.name != "env-cluster" || o.workers != 3 {
		t.Fatal("CLI > env > YAML precedence failed", o)
	}
	if !strings.HasSuffix(o.kubeconfig, "kind-env-cluster.config") {
		t.Fatal(o.kubeconfig)
	}
}
func TestConfigInitDoesNotShadowLegacy(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("opskit.yaml", []byte("schema_version: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	code, err, _ := workflowRun(t, "config", "init")
	if code == 0 || err == nil {
		t.Fatal("legacy config silently shadowed")
	}
	if _, err = os.Stat(defaultConfigPath); !os.IsNotExist(err) {
		t.Fatal("created second config")
	}
}

func TestDockerMirrorBackupAndPreview(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	target := filepath.Join(dir, "daemon.json")
	if err := os.WriteFile(config, []byte("schema_version: 1\nmirrors:\n docker: [https://registry.example.com]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	old := []byte(`{"log-driver":"json-file","registry-mirrors":["https://old.example.com"]}`)
	if err := os.WriteFile(target, old, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"mirror", "docker", "--config", config, "--file", target}
	code, err, _ := workflowRun(t, append(args, "--dry-run")...)
	if code != 0 || err != nil {
		t.Fatal(code, err)
	}
	raw, _ := os.ReadFile(target)
	if !bytes.Equal(raw, old) {
		t.Fatal("preview modified file")
	}
	code, err, _ = workflowRun(t, args...)
	if code != 0 || err != nil {
		t.Fatal(code, err)
	}
	raw, _ = os.ReadFile(target)
	var data map[string]any
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if data["log-driver"] != "json-file" {
		t.Fatal("unrelated settings changed")
	}
	backups, _ := filepath.Glob(target + ".backup-*")
	if len(backups) != 1 {
		t.Fatal("backup missing")
	}
	raw, _ = os.ReadFile(backups[0])
	if !bytes.Equal(raw, old) {
		t.Fatal("backup content changed")
	}
}

func TestMirrorPreviewDoesNotPrintExistingSecrets(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := t.TempDir()
	target := filepath.Join(dir, ".npmrc")
	if err := os.WriteFile(target, []byte("//registry.example.com/:_authToken=DO-NOT-PRINT\nregistry=https://old.example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	code, err, text := workflowRun(t, "mirror", "npm", "--file", target, "--url", "https://new.example.com", "--dry-run")
	if code != 0 || err != nil {
		t.Fatal(code, err)
	}
	if strings.Contains(text, "DO-NOT-PRINT") {
		t.Fatal("existing auth token leaked in preview")
	}
	code, err, _ = workflowRun(t, "mirror", "npm", "--file", target, "--url", "https://new.example.com")
	if code != 0 || err != nil {
		t.Fatal(code, err)
	}
	raw, _ := os.ReadFile(target)
	if !strings.Contains(string(raw), "DO-NOT-PRINT") {
		t.Fatal("existing token removed")
	}
}

func TestContextSwitchRestoresStateWithoutPrintingCredentials(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := fakeTools(t)
	target := filepath.Join(dir, "user.config")
	kindConfig := filepath.Join(dir, "kind.config")
	if err := os.WriteFile(target, []byte("original-user-config"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kindConfig, []byte("kind-user-config"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPSKIT_USER_KUBECONFIG", target)
	var out bytes.Buffer
	o := clusterOptions{name: "test", kubeconfig: kindConfig}
	if err := switchKindContext(context.Background(), o, "use", &out); err != nil {
		t.Fatal(err)
	}
	state, err := os.ReadFile(kindConfig + ".previous-context")
	if err != nil || string(state) != "original-context" {
		t.Fatal(string(state), err)
	}
	if strings.Contains(out.String(), "EXISTING-TOKEN") {
		t.Fatal("merged credentials printed")
	}
	raw, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(raw), "EXISTING-TOKEN") {
		t.Fatal("credentials lost", err)
	}
	if err = switchKindContext(context.Background(), o, "unuse", &out); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(kindConfig + ".previous-context"); !os.IsNotExist(err) {
		t.Fatal("stale context state retained")
	}
	events, _ := os.ReadFile(filepath.Join(dir, "events"))
	if !strings.Contains(string(events), "config use-context original-context") {
		t.Fatal("previous context not restored")
	}
}

func TestProjectTemplateDefaultsAreUsable(t *testing.T) {
	t.Chdir(t.TempDir())
	code, err, _ := workflowRun(t, "config", "init")
	if code != 0 || err != nil {
		t.Fatal(code, err)
	}
	c, err := readFileConfig(defaultConfigPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Mirrors.Docker) != 2 || len(c.Mirrors.Containerd) != 6 || len(c.Mirrors.DockerInsecure) != 4 {
		t.Fatal("project runtime defaults missing")
	}
	if c.Mirrors.Containerd["docker.io"].Endpoint != "https://huball.ihome.sxxpqp.top:8443/v2/dockerhub" {
		t.Fatal("wrong dockerhub endpoint")
	}
	if c.Mirrors.Containerd["registry.cn-hangzhou.aliyuncs.com"].Endpoint != "" {
		t.Fatal("ACR should stay direct")
	}
	if _, err = renderHosts(c.Mirrors.Containerd); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"docker", "go", "npm", "pip", "maven"} {
		file := filepath.Join(t.TempDir(), tool+".config")
		code, err, _ = workflowRun(t, "mirror", tool, "--file", file, "--dry-run")
		if code != 0 || err != nil {
			t.Fatal(tool, code, err)
		}
		if _, err = os.Stat(file); !os.IsNotExist(err) {
			t.Fatal("preview wrote config")
		}
	}
	if c.KubeSphere.InstallerSource != "registry.cn-beijing.aliyuncs.com/kubesphereio/ks-installer:v3.4.1" {
		t.Fatal("installer default missing")
	}
}
func TestGoProxyList(t *testing.T) {
	for _, source := range []string{"https://goproxy.cn,direct", "https://first.example.com|https://second.example.com,direct", "direct", "off"} {
		text, err := renderLanguage("go", nil, source)
		if err != nil || !strings.Contains(string(text), "GOPROXY="+source) {
			t.Fatal(source, err, string(text))
		}
	}
	for _, source := range []string{",direct", "https://ok.example.com,,direct", "https://ok.example.com,ftp://bad.example.com", "https://user:password@example.com,direct"} {
		if _, err := renderLanguage("go", nil, source); err == nil {
			t.Fatal("invalid proxy accepted", source)
		}
	}
}
func TestDockerInsecureMerge(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	target := filepath.Join(dir, "daemon.json")
	if err := os.WriteFile(config, templates.ConfigTemplate, 0600); err != nil {
		t.Fatal(err)
	}
	old := []byte(`{"log-driver":"local","insecure-registries":["existing.internal:5000","dockerhub.ihome.sxxpqp.top:8443"]}`)
	if err := os.WriteFile(target, old, 0600); err != nil {
		t.Fatal(err)
	}
	code, err, _ := workflowRun(t, "mirror", "docker", "--config", config, "--file", target)
	if code != 0 || err != nil {
		t.Fatal(code, err)
	}
	raw, _ := os.ReadFile(target)
	var data map[string]any
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	items := data["insecure-registries"].([]any)
	if len(items) != 5 || data["log-driver"] != "local" {
		t.Fatal(string(raw))
	}
}
func TestSelfHostedIgnoresKindInstallerTemplate(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := fakeTools(t)
	config := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(config, templates.ConfigTemplate, 0600); err != nil {
		t.Fatal(err)
	}
	code, err, text := workflowRun(t, "kubesphere", "install", "--config", config, "--kubeconfig", filepath.Join(dir, "kube.config"), "--context", "self-hosted", "--dry-run")
	if code != 0 || err != nil {
		t.Fatal(code, err, text)
	}
	events, _ := os.ReadFile(filepath.Join(dir, "events"))
	if strings.Contains(string(events), "docker ") {
		t.Fatal("self-hosted preflight tried kind preload")
	}
}

func TestMirrorRejectsApply(t *testing.T) {
	code, err, _ := workflowRun(t, "mirror", "docker", "--apply")
	if code == 0 || err == nil {
		t.Fatal("removed --apply flag accepted")
	}
}

func TestKubeSphereAutoContext(t *testing.T) {
	for _, env := range []string{"", "/tmp/first.config" + string(os.PathListSeparator) + "/tmp/second.config"} {
		t.Run(env, func(t *testing.T) {
			t.Chdir(t.TempDir())
			dir := fakeTools(t)
			t.Setenv("KUBECONFIG", env)
			t.Setenv("OPSKIT_FAKE_CURRENT_CONTEXT", "kind-test")
			code, err, text := workflowRun(t, "kubesphere", "install", "--version", "v3.4.1", "--dry-run")
			if code != 0 || err != nil {
				t.Fatal(code, err, text)
			}
			events, _ := os.ReadFile(filepath.Join(dir, "events"))
			if !strings.Contains(string(events), "kubectl config current-context") || !strings.Contains(string(events), "--context kind-test") || strings.Contains(string(events), "--kubeconfig") {
				t.Fatal(string(events))
			}
			t.Setenv("OPSKIT_FAKE_CURRENT_CONTEXT", "")
			code, err, _ = workflowRun(t, "kubesphere", "install", "--version", "v3.4.1", "--dry-run")
			if code == 0 || err == nil || !strings.Contains(err.Error(), "current-context") {
				t.Fatal(code, err)
			}
		})
	}
}

func TestKubeSphereRejectsApply(t *testing.T) {
	code, err, _ := workflowRun(t, "kubesphere", "install", "--apply")
	if code == 0 || err == nil {
		t.Fatal("removed --apply flag accepted")
	}
}

func TestDeleteDefaultExecutionAndDryRun(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := fakeTools(t)
	config := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(config, []byte("schema_version: 1\nkind:\n  name: test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"kind", "delete", "--config", config}
	code, err, _ := workflowRun(t, append(args, "--dry-run")...)
	if code != 0 || err != nil {
		t.Fatal(code, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "events")); !os.IsNotExist(err) {
		t.Fatal("preview executed command")
	}
	code, err, _ = workflowRun(t, append(args, "--apply")...)
	if code == 0 || err == nil {
		t.Fatal("removed --apply accepted")
	}
	code, err, _ = workflowRun(t, args...)
	if code != 0 || err != nil {
		t.Fatal(code, err)
	}
	events, _ := os.ReadFile(filepath.Join(dir, "events"))
	if !strings.Contains(string(events), "kind delete cluster --name test") {
		t.Fatal(string(events))
	}
}
