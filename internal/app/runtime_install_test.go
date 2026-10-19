package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRuntimeInstallPlans(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if e := os.WriteFile(cfg, []byte("install:\n    cni_plugins:\n        download_base: https://packages.example\n    containerd:\n        download_base: https://packages.example\n    cri_dockerd:\n        download_base: https://packages.example\n    runc:\n        download_base: https://packages.example\nk8s:\n    cniPlugins:\n        version: v1.5.1\n    containerd:\n        version: v2.1.3\n    criDockerd:\n        version: v0.3.22\n    docker:\n        version: v27.3.1\n    runc:\n        version: v1.2.6\nschema_version: 1\n"), 0600); e != nil {
		t.Fatal(e)
	}
	cases := []struct {
		tool string
		args []string
		want []string
	}{
		{"docker", []string{"--mirror", "Aliyun"}, []string{"官方脚本 --mirror Aliyun --version 27.3.1", "仅预览", "/etc/sysctl.d/99-inotify.conf", "8192", "524288"}},
		{"docker", []string{"--method", "binary", "--arch", "arm64", "--version", "28.0.1"}, []string{"docker v28.0.1", "aarch64/docker-28.0.1.tgz", "/etc/sysctl.d/99-inotify.conf"}},
		{"cri-dockerd", []string{"--arch", "amd64"}, []string{"cri-dockerd-0.3.22.amd64.tgz", "https://packages.example/Mirantis"}},
		{"containerd", []string{"--arch", "arm64", "--runc-version", "v1.2.5"}, []string{"containerd-2.1.3-linux-arm64.tar.gz", "runc v1.2.5", "cni-plugins-linux-arm64-v1.5.1.tgz"}},
	}
	for _, c := range cases {
		t.Run(c.tool+strings.Join(c.args, " "), func(t *testing.T) {
			var out, diag bytes.Buffer
			args := append([]string{c.tool, "install", "--config", cfg, "--dry-run"}, c.args...)
			code, e := Run(context.Background(), args, &out, &diag, "dev")
			if e != nil || code != 0 {
				t.Fatalf("%d %v %s", code, e, diag.String())
			}
			for _, s := range c.want {
				if !strings.Contains(out.String(), s) {
					t.Fatalf("missing %q in %s", s, out.String())
				}
			}
		})
	}
	for _, args := range [][]string{{"docker", "install", "--version", "latest"}, {"docker", "install", "--version", "v27.3.1", "--method", "bad"}, {"docker", "install", "--version", "v27.3.1", "--mirror", "ALiyun"}, {"containerd", "install", "--version", "v3.0.0"}, {"cri-dockerd", "install", "--version", "v0.3.22", "--url", "https://user:secret@example/a"}, {"docker", "install", "--version", "v27.3.1", "--sha256", "bad"}} {
		var out, diag bytes.Buffer
		if code, e := Run(context.Background(), append(args, "--config", cfg), &out, &diag, "dev"); code == 0 || e == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func runtimeTestArchive(t *testing.T, entries []*tar.Header) []byte {
	t.Helper()
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	w := tar.NewWriter(z)
	for _, h := range entries {
		if e := w.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if h.Typeflag == tar.TypeReg {
			if _, e := w.Write([]byte("bin")); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestRuntimeArchiveRejectsUnsafeEntries(t *testing.T) {
	good := runtimeTestArchive(t, []*tar.Header{{Name: "bin/containerd", Mode: 0755, Size: 3, Typeflag: tar.TypeReg}})
	files, e := runtimeArchive(good)
	if e != nil || string(files["containerd"]) != "bin" {
		t.Fatalf("%v %v", files, e)
	}
	for _, name := range []string{"../escape", "/root/escape", "dir/../../escape", "dir\\escape"} {
		b := runtimeTestArchive(t, []*tar.Header{{Name: name, Mode: 0755, Size: 3, Typeflag: tar.TypeReg}})
		if _, e := runtimeArchive(b); e == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	for _, entries := range [][]*tar.Header{{{Name: "bin/tool", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}}, {{Name: "bin/tool", Size: 3, Typeflag: tar.TypeReg}, {Name: "other/tool", Size: 3, Typeflag: tar.TypeReg}}} {
		if _, e := runtimeArchive(runtimeTestArchive(t, entries)); e == nil {
			t.Fatal("accepted unsafe archive")
		}
	}
}
func TestRuntimeContainerdConfig(t *testing.T) {
	for _, plugin := range []string{"io.containerd.grpc.v1.cri", "io.containerd.cri.v1.images"} {
		in := "version = 2\n[plugins.'" + plugin + "'.registry]\n config_path = ''\n[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc.options]\n SystemdCgroup = false\n"
		out, e := runtimeContainerdConfig(in)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(out, "SystemdCgroup = true") || !strings.Contains(out, `config_path = "/etc/containerd/certs.d"`) {
			t.Fatal(out)
		}
	}
}
func TestRuntimeWriteDoesNotOverwrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tool")
	if e := writeRuntimeNew(p, []byte("first"), 0755); e != nil {
		t.Fatal(e)
	}
	if e := writeRuntimeNew(p, []byte("second"), 0755); e == nil {
		t.Fatal("overwrote existing file")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "first" {
		t.Fatal(string(b))
	}
	if ensureRuntimeAbsent(p) == nil {
		t.Fatal("existing file accepted")
	}
	if ensureRuntimeAbsent(p+"-new") != nil {
		t.Fatal("missing path rejected")
	}
}
func TestRuntimeChecksumAndUnits(t *testing.T) {
	b := []byte("archive")
	sum := sha256.Sum256(b)
	if e := checkRuntimeHash(b, hex.EncodeToString(sum[:])); e != nil {
		t.Fatal(e)
	}
	if e := checkRuntimeHash([]byte("tampered"), hex.EncodeToString(sum[:])); e == nil {
		t.Fatal("accepted tampering")
	}
	u := runtimeUnits("cri-dockerd")["cri-docker.service"]
	if !strings.Contains(u, "unix:///run/cri-dockerd.sock") || strings.Contains(u, "fd://") {
		t.Fatal(u)
	}
}

func TestRuntimeInstallExecutesByDefault(t *testing.T) {
	// On non-Linux hosts, default execution must reach the platform preflight;
	// dry-run succeeds before that preflight without touching the host.
	if runtime.GOOS == "linux" {
		t.Skip("non-Linux preflight test")
	}
	cfg := filepath.Join(t.TempDir(), "absent.yaml")
	for _, tool := range []string{"docker", "cri-dockerd", "containerd"} {
		var out, diag bytes.Buffer
		code, err := Run(context.Background(), []string{tool, "install", "--config", cfg, "--version", "v1.2.3"}, &out, &diag, "dev")
		// Explicit config paths must exist.
		if err == nil || code == 0 {
			t.Fatal("expected missing config error")
		}
		if err := os.WriteFile(cfg, []byte("schema_version: 1\n"), 0600); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		diag.Reset()
		code, err = Run(context.Background(), []string{tool, "install", "--config", cfg, "--version", "v1.2.3"}, &out, &diag, "dev")
		if code == 0 || err == nil || !strings.Contains(err.Error(), "Linux") {
			t.Fatalf("default execution did not reach preflight: %s %d %v", tool, code, err)
		}
		out.Reset()
		diag.Reset()
		code, err = Run(context.Background(), []string{tool, "install", "--config", cfg, "--version", "v1.2.3", "--apply"}, &out, &diag, "dev")
		if code == 0 || err == nil {
			t.Fatal("removed --apply flag accepted")
		}
		if err := os.Remove(cfg); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRuntimeDownloadDefaultsIgnoreKindEnvironment(t *testing.T) {
	t.Setenv("KIND_DOWNLOAD_BASE", "https://kind-only.example")
	defaults, err := defaultInstallSettings()
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("schema_version: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"containerd", "cri-dockerd"} {
		var out bytes.Buffer
		code, err := Run(context.Background(), append([]string{tool, "install"}, []string{"--config", cfg, "--version", "v1.7.27", "--dry-run"}...), &out, &out, "test")
		if code != 0 || err != nil || !strings.Contains(out.String(), defaults.Containerd.DownloadBase+"/") || strings.Contains(out.String(), "kind-only.example") {
			t.Fatalf("%s: %d %v %s", tool, code, err, out.String())
		}
	}
}

func TestDockerScriptCustomMirror(t *testing.T) {
	t.Setenv("DOWNLOAD_URL", "https://wrong.example")
	mirror := "https://mirrors.tuna.tsinghua.edu.cn/docker-ce/"
	if err := validateDockerMirror(mirror); err != nil {
		t.Fatal(err)
	}
	cmd := dockerScriptCommand(context.Background(), "installer.sh", "27.3.1", mirror)
	args := strings.Join(cmd.Args, " ")
	if strings.Contains(args, "--mirror") || !strings.Contains(args, "--version 27.3.1") {
		t.Fatal(args)
	}
	count := 0
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "DOWNLOAD_URL=") {
			count++
			if entry != "DOWNLOAD_URL=https://mirrors.tuna.tsinghua.edu.cn/docker-ce" {
				t.Fatal(entry)
			}
		}
	}
	if count != 1 {
		t.Fatalf("DOWNLOAD_URL count=%d", count)
	}
	for _, invalid := range []string{"Tuna", "https://user:password@example.com", "https://example.com?a=1", "file:///tmp/repo"} {
		if validateDockerMirror(invalid) == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
}

func TestInstallComponentDownloadIsolation(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	body := "install:\n    cni_plugins:\n        download_base: https://cni.example\n    containerd:\n        download_base: https://containerd.example\n    cri_dockerd:\n        download_base: https://cri.example\n    docker:\n        binary_base: https://docker.example\n        method: binary\n    runc:\n        download_base: https://runc.example\nk8s:\n    cniPlugins:\n        version: v1.5.1\n    containerd:\n        version: v2.1.3\n    criDockerd:\n        version: v0.3.22\n    docker:\n        version: v27.3.1\n    runc:\n        version: v1.2.6\nschema_version: 1\n"
	if err := os.WriteFile(cfg, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tool string
		want []string
	}{
		{"containerd", []string{"https://containerd.example/containerd/", "https://runc.example/opencontainers/", "https://cni.example/containernetworking/"}},
		{"cri-dockerd", []string{"https://cri.example/Mirantis/"}},
		{"docker", []string{"https://docker.example/x86_64/docker-27.3.1.tgz"}},
	} {
		var out bytes.Buffer
		code, err := Run(context.Background(), append([]string{tc.tool, "install"}, []string{"--config", cfg, "--arch", "amd64", "--dry-run"}...), &out, &out, "test")
		if code != 0 || err != nil {
			t.Fatalf("%s: %d %v %s", tc.tool, code, err, out.String())
		}
		for _, want := range tc.want {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("%s missing %s: %s", tc.tool, want, out.String())
			}
		}
	}
}

func TestRemovedFlatInstallKeysRejected(t *testing.T) {
	for _, key := range []string{"kind_download_base", "kubectl_download_base", "docker_method", "docker_mirror", "docker_script", "docker_binary_base", "release_base"} {
		t.Run(key, func(t *testing.T) {
			cfg := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(cfg, []byte("schema_version: 1\ninstall:\n  "+key+": old\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := readFileConfig(cfg, true); err == nil {
				t.Fatal("removed flat install key accepted")
			}
		})
	}
}
