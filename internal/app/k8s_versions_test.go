package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	templates "github.com/sxxpqp/opskit"
)

func TestClusterComponentDefaults(t *testing.T) {
	for _, tc := range []struct{ kube, cri string }{
		{"v1.24.7", "v1.24.2"}, {"v1.25.16", "v1.25.0"}, {"v1.26.15", "v1.26.1"},
		{"v1.27.16", "v1.27.1"}, {"v1.28.15", "v1.28.0"}, {"v1.29.10", "v1.29.0"}, {"v1.30.0", "v1.30.1"},
	} {
		var c clusterSettings
		c.Kubernetes.Version = tc.kube
		if err := applyClusterComponentDefaults(&c); err != nil {
			t.Fatal(err)
		}
		wantContainerd, wantRunc, wantDocker := "v1.7.28", "v1.3.3", "v28.5.2"
		if mustMinor(t, tc.kube) >= 30 {
			wantContainerd, wantRunc, wantDocker = "v2.2.6", "v1.3.6", "v29.6.2"
		}
		if c.CRITools.Version != tc.cri || c.Containerd.Version != wantContainerd || c.Runc.Version != wantRunc || c.Docker.Version != wantDocker || c.CNIPlugins.Version != "v1.9.1" || c.CRIDockerd.Version != "v0.4.7" {
			t.Fatalf("bad profile: %+v", c)
		}
		if err := checkKubeadmContainerd(mustMinor(t, tc.kube), c.Containerd.Version); err != nil {
			t.Fatal(err)
		}
	}
	var c clusterSettings
	c.Kubernetes.Version = "v1.24.7"
	c.Containerd.Version = "v2.1.3"
	if err := applyClusterComponentDefaults(&c); err != nil {
		t.Fatal(err)
	}
	if c.Containerd.Version != "v2.1.3" {
		t.Fatal("explicit version overridden")
	}
	c.Kubernetes.Version = "v1.31.0"
	if err := applyClusterComponentDefaults(&c); err == nil {
		t.Fatal("unsupported Kubernetes accepted")
	}
}
func mustMinor(t *testing.T, version string) int {
	t.Helper()
	m, err := kubeadmMinor(version)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestClusterComponentDefaultsCLIOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, templates.ConfigTemplate, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"k8s", "create", "--config", path, "--version", "v1.28.15", "--dry-run"},
		{"kubeadm", "install", "--config", path, "--version", "v1.28.15", "--dry-run"},
	} {
		var out bytes.Buffer
		code, err := Run(context.Background(), args, &out, io.Discard, "test")
		if code != 0 || err != nil || !strings.Contains(out.String(), "v1.28.0") {
			t.Fatalf("%v: %d %v %s", args, code, err, out.String())
		}
	}
}
