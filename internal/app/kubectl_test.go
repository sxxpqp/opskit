package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sxxpqp/opskit/internal/k8s"
)

func TestKubectlAssets(t *testing.T) {
	for _, platform := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name, err := k8s.KubectlAsset(platform, arch)
			if platform == "windows" && arch == "arm64" {
				if err == nil {
					t.Fatal("unsupported Windows arm64 accepted")
				}
				continue
			}
			if err != nil || (platform == "windows") != strings.HasSuffix(name, ".exe") {
				t.Fatalf("%s/%s: %s %v", platform, arch, name, err)
			}
		}
	}
	if _, err := k8s.KubectlAsset("linux", "386"); err == nil {
		t.Fatal("386 accepted")
	}
}
func TestKubectlHelpAndPlan(t *testing.T) {
	t.Setenv("KUBECTL_DOWNLOAD_BASE", "")
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "")
	for _, args := range [][]string{{"kubectl"}, {"kubectl", "-h"}, {"kubectl", "--help"}, {"kubectl", "install", "-h"}, {"kubectl", "install", "--help"}} {
		var out bytes.Buffer
		code, err := Run(context.Background(), args, &out, &out, "dev")
		if code != 0 || err != nil || !strings.Contains(out.String(), "version") {
			t.Fatalf("%v %d %v %s", args, code, err, out.String())
		}
	}
	if _, err := k8s.KubectlAsset(runtime.GOOS, runtime.GOARCH); err != nil {
		t.Skip("installation platform unsupported; help verified")
	}
	var missing bytes.Buffer
	if code, err := Run(context.Background(), []string{"kubectl", "install", "--dry-run"}, &missing, &missing, "dev"); code == 0 || err == nil {
		t.Fatal("implicit version was selected without configuration")
	}
	config := "config.yaml"
	if err := os.WriteFile(config, []byte("schema_version: 1\nversions:\n  kubernetes: v1.24.7\ninstall:\n  kubectl_download_base: https://packages.example/k8s\n  download_dir: downloads\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"", "v1.25.0"} {
		args := []string{"kubectl", "install", "--config", config, "--dry-run"}
		want := "v1.24.7"
		if version != "" {
			want = version
			args = append(args, "--version", version)
		}
		var out bytes.Buffer
		code, err := Run(context.Background(), args, &out, &out, "dev")
		if code != 0 || err != nil || !strings.Contains(out.String(), "https://packages.example/k8s/"+want+"/bin/") || !strings.Contains(out.String(), "未下载") {
			t.Fatalf("%d %v %s", code, err, out.String())
		}
	}
	if err := os.WriteFile(config, []byte("schema_version: 1\nversions:\n  kubectl: v1.23.9\n  kubernetes: v1.24.7\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code, err := Run(context.Background(), []string{"kubectl", "install", "--config", config, "--dry-run"}, &out, &out, "dev")
	if code != 0 || err != nil || !strings.Contains(out.String(), "kubectl install: v1.23.9") {
		t.Fatalf("%d %v %s", code, err, out.String())
	}
	for _, extra := range [][]string{{"--version", "latest"}, {"--version", "v1.24.7", "--download-base", "https://user:secret@example.com"}, {"--version", "v1.24.7", "--apply"}, {"--version", "v1.24.7", "unexpected"}} {
		args := append([]string{"kubectl", "install", "--config", config, "--dry-run"}, extra...)
		out.Reset()
		if code, err := Run(context.Background(), args, &out, &out, "dev"); code == 0 || err == nil {
			t.Fatalf("bad arguments accepted: %v", args)
		}
	}
}
func TestKubectlInstallVerifiedAndFailurePreservesOriginal(t *testing.T) {
	if _, err := k8s.KubectlAsset(runtime.GOOS, runtime.GOARCH); err != nil {
		t.Skip(err)
	}
	fakeTools(t)
	root := t.TempDir()
	entry := filepath.Join(root, "bin")
	if err := os.Mkdir(entry, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", entry)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", root)
	} else {
		t.Setenv("HOME", root)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	var failure atomic.Int32
	binary, _ := k8s.KubectlAsset(runtime.GOOS, runtime.GOARCH)
	expectedPath := "/v1.24.7/bin/" + runtime.GOOS + "/" + runtime.GOARCH + "/" + binary
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.URL.Path != expectedPath && r.URL.Path != expectedPath+".sha256" {
			http.NotFound(w, r)
			return
		}
		if failure.Load() == 2 {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			payload := data
			if failure.Load() == 1 {
				payload = []byte("corrupt")
			}
			fmt.Fprintf(w, "%x", sha256.Sum256(payload))
			return
		}
		w.Write(data)
	}))
	defer server.Close()
	config := filepath.Join(root, "config.yaml")
	if err = os.WriteFile(config, []byte("schema_version: 1\nversions:\n  kubernetes: v1.24.7\n"), 0600); err != nil {
		t.Fatal(err)
	}
	targetDir := filepath.Join(root, "installed")
	args := []string{"kubectl", "install", "--config", config, "--bin-dir", targetDir, "--download-base", server.URL}
	var out bytes.Buffer
	for i := 0; i < 2; i++ {
		out.Reset()
		code, err := Run(context.Background(), args, &out, &out, "dev")
		if code != 0 || err != nil || !strings.Contains(out.String(), "PATH 验证通过") {
			t.Fatalf("%d %v %s", code, err, out.String())
		}
	}
	if count.Load() != 2 {
		t.Fatalf("reinstalled same version: %d requests", count.Load())
	}
	target := filepath.Join(targetDir, binary)
	original, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	// Make installed file's reported version fail so replacement is attempted; failure must preserve it.
	t.Setenv("OPSKIT_FAKE_KUBECTL_VERSION", "v0.0.1")
	for _, mode := range []int32{1, 2, 0} {
		failure.Store(mode)
		out.Reset()
		code, err := Run(context.Background(), args, &out, &out, "dev")
		if code == 0 || err == nil {
			t.Fatalf("failure mode %d accepted", mode)
		}
		now, err := os.ReadFile(target)
		if err != nil || !bytes.Equal(now, original) {
			t.Fatalf("failure mode %d changed original: %v", mode, err)
		}
	}
	t.Setenv("OPSKIT_FAKE_KUBECTL_VERSION", "")
	t.Setenv("OPSKIT_FAKE_KUBECTL_JSON", "invalid")
	if err = k8s.VerifyKubectl(context.Background(), target, "v1.24.7"); err == nil {
		t.Fatal("malformed version JSON accepted")
	}
	files, _ := filepath.Glob(filepath.Join(root, ".opskit", "downloads", "kubectl", "v1.24.7", "*", binary))
	if len(files) != 1 {
		t.Fatalf("downloads not retained: %v", files)
	}
	for _, name := range []string{".bashrc", ".profile", ".zshrc"} {
		if _, err = os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("unnecessary profile write: %s", name)
		}
	}
}

func TestKubectlDownloadSourcePrecedence(t *testing.T) {
	if _, err := k8s.KubectlAsset(runtime.GOOS, runtime.GOARCH); err != nil {
		t.Skip(err)
	}
	cases := []struct{ name, config, env, cli, want string }{
		{"default", "", "", "", defaultKubectlDownloadBase},
		{"yaml", "https://config.example/release", "", "", "https://config.example/release"},
		{"environment", "https://config.example/release", "https://env.example/release", "", "https://env.example/release"},
		{"cli", "https://config.example/release", "https://env.example/release", "https://cli.example/release", "https://cli.example/release"},
		{"empty environment", "https://config.example/release", "", "", "https://config.example/release"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("KUBECTL_DOWNLOAD_BASE", c.env)
			file := filepath.Join(t.TempDir(), "config.yaml")
			text := "schema_version: 1\ninstall:\n  kubectl_download_base: " + fmt.Sprintf("%q", c.config) + "\n"
			if err := os.WriteFile(file, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"kubectl", "install", "--config", file, "--version", "v1.24.7", "--dry-run"}
			if c.cli != "" {
				args = append(args, "--download-base", c.cli)
			}
			var out bytes.Buffer
			code, err := Run(context.Background(), args, &out, &out, "dev")
			if code != 0 || err != nil || !strings.Contains(out.String(), c.want+"/v1.24.7/bin/") {
				t.Fatalf("%d %v %s", code, err, out.String())
			}
		})
	}
	t.Setenv("KUBECTL_DOWNLOAD_BASE", "https://user:secret@bad.example/release")
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("schema_version: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code, err := Run(context.Background(), []string{"kubectl", "install", "--config", cfg, "--version", "v1.24.7", "--dry-run"}, &out, &out, "dev")
	if code == 0 || err == nil {
		t.Fatal("invalid environment source silently ignored")
	}
	if strings.Contains(out.String(), "secret") {
		t.Fatal("source credentials leaked")
	}
}
