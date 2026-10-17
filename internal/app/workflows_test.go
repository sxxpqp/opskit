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
		case strings.Contains(args, "config view -o jsonpath="):
			fmt.Print("original-context")
		case args == "config view --raw --flatten":
			fmt.Print("apiVersion: v1\nkind: Config\nusers:\n- name: original\n  user:\n    token: EXISTING-TOKEN\ncontexts:\n- name: original-context\n- name: kind-test\n")
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
		if e = os.Link(exe, path); e != nil {
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
func workflowRun(t *testing.T, args ...string) (int, error, string) {
	t.Helper()
	var output bytes.Buffer
	code, err := Run(context.Background(), args, &output, &output, "test")
	return code, err, output.String()
}
func TestCreatePreviewApplyAndExisting(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := fakeTools(t)
	config := filepath.Join(dir, "kube.config")
	args := []string{"kind", "create", "--name", "test", "--version", "v1.24.7", "--workers", "2", "--kubeconfig", config}
	code, err, text := workflowRun(t, args...)
	if code != 0 || err != nil {
		t.Fatal(code, err)
	}
	if _, e := os.Stat(filepath.Join(dir, "events")); !os.IsNotExist(e) {
		t.Fatal("preview executed commands")
	}
	if !strings.Contains(text, "kindest/node:v1.24.7") {
		t.Fatal(text)
	}
	code, err, text = workflowRun(t, append(args, "--apply")...)
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
	code, err, _ = workflowRun(t, append(args, "--apply")...)
	if code == 0 || err == nil {
		t.Fatal("existing cluster overwritten")
	}
}
func TestDeleteRequiresExactName(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := fakeTools(t)
	code, err, _ := workflowRun(t, "kind", "delete", "--name", "test", "--apply", "--confirm-name", "wrong")
	if code == 0 || err == nil {
		t.Fatal("delete without matching confirmation")
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
	args := []string{"mirror", "containerd", "--kind", "test", "--config", config, "--apply"}
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
func TestKubeSpherePreflightApplyAndProtection(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := fakeTools(t)
	secret := filepath.Join(dir, "password.secret")
	args := []string{"kubesphere", "install", "--version", "v3.4.1", "--kind", "test", "--password-file", secret}
	code, err, text := workflowRun(t, args...)
	if code != 0 || err != nil {
		t.Fatal(code, err, text)
	}
	if _, e := os.Stat(secret); !os.IsNotExist(e) {
		t.Fatal("preview generated password")
	}
	t.Setenv("OPSKIT_FAKE_K8S", "v1.35.0")
	code, err, _ = workflowRun(t, append(args, "--apply")...)
	if code == 0 || err == nil {
		t.Fatal("unsupported K8s installed")
	}
	t.Setenv("OPSKIT_FAKE_K8S", "v1.24.7")
	t.Setenv("OPSKIT_FAKE_KS_EXISTS", "yes")
	code, err, _ = workflowRun(t, append(args, "--apply")...)
	if code == 0 || err == nil {
		t.Fatal("existing KS overwritten")
	}
	t.Setenv("OPSKIT_FAKE_KS_EXISTS", "")
	code, err, text = workflowRun(t, append(args, "--apply")...)
	if code != 0 || err != nil {
		t.Fatal(code, err, text)
	}
	password, e := os.ReadFile(secret)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(text, strings.TrimSpace(string(password))) {
		t.Fatal("password leaked")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "applied.yaml"))
	if !strings.Contains(string(raw), "openebs-hostpath") || !strings.Contains(string(raw), "adminPassword:") {
		t.Fatal("runtime configuration not injected")
	}
	events, _ := os.ReadFile(filepath.Join(dir, "events"))
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
	code, err, _ := workflowRun(t, args...)
	if code != 0 || err != nil {
		t.Fatal(code, err)
	}
	raw, _ := os.ReadFile(target)
	if !bytes.Equal(raw, old) {
		t.Fatal("preview modified file")
	}
	code, err, _ = workflowRun(t, append(args, "--apply")...)
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
	code, err, text := workflowRun(t, "mirror", "npm", "--file", target, "--url", "https://new.example.com")
	if code != 0 || err != nil {
		t.Fatal(code, err)
	}
	if strings.Contains(text, "DO-NOT-PRINT") {
		t.Fatal("existing auth token leaked in preview")
	}
	code, err, _ = workflowRun(t, "mirror", "npm", "--file", target, "--url", "https://new.example.com", "--apply")
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
