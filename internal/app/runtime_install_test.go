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
	"strings"
	"testing"
)

func TestRuntimeInstallPlans(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if e := os.WriteFile(cfg, []byte("schema_version: 1\nversions:\n  docker: v27.3.1\n  cri_dockerd: v0.3.22\n  containerd: v2.1.3\n  runc: v1.2.6\n  cni_plugins: v1.5.1\ninstall:\n  release_base: https://packages.example\n"), 0600); e != nil {
		t.Fatal(e)
	}
	cases := []struct {
		tool string
		args []string
		want []string
	}{
		{"docker", nil, []string{"官方脚本 --mirror Aliyun --version 27.3.1", "仅预览"}},
		{"docker", []string{"--method", "binary", "--arch", "arm64", "--version", "28.0.1"}, []string{"docker v28.0.1", "aarch64/docker-28.0.1.tgz"}},
		{"cri-dockerd", []string{"--arch", "amd64"}, []string{"cri-dockerd-0.3.22.amd64.tgz", "https://packages.example/Mirantis"}},
		{"containerd", []string{"--arch", "arm64", "--runc-version", "v1.2.5"}, []string{"containerd-2.1.3-linux-arm64.tar.gz", "runc v1.2.5", "cni-plugins-linux-arm64-v1.5.1.tgz"}},
	}
	for _, c := range cases {
		t.Run(c.tool+strings.Join(c.args, " "), func(t *testing.T) {
			var out, diag bytes.Buffer
			args := append([]string{c.tool, "install", "--config", cfg}, c.args...)
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
