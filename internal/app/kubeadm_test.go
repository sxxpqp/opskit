package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	templates "github.com/sxxpqp/opskit"
	"gopkg.in/yaml.v3"
)

func kubeadmTestConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, templates.ConfigTemplate, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestKubeadmDryRunAndPrecedence(t *testing.T) {
	t.Chdir(t.TempDir())
	config := kubeadmTestConfig(t)
	t.Setenv("PATH", "")
	t.Setenv("KUBEADM_DOWNLOAD_BASE", "https://kubeadm.example/release")
	t.Setenv("KUBELET_DOWNLOAD_BASE", "https://kubelet.example/release")
	code, err, text := workflowRun(t, "kubeadm", "install", "--config", config, "--dry-run")
	if code != 0 || err != nil {
		t.Fatal(code, err, text)
	}
	if !strings.Contains(text, "https://kubeadm.example/release/v1.24.7/bin/linux/") || !strings.Contains(text, "https://kubelet.example/release/v1.24.7/bin/linux/") {
		t.Fatal(text)
	}
	if strings.Contains(text, "kubectl: https://kubeadm.example") || strings.Contains(text, "crictl: https://kubeadm.example") {
		t.Fatal("component environment leaked", text)
	}
	code, err, text = workflowRun(t, "kubeadm", "install", "--config", config, "--kubeadm-download-base", "https://cli.example", "--dry-run")
	if code != 0 || err != nil || !strings.Contains(text, "kubeadm: https://cli.example/v1.24.7/") {
		t.Fatal(code, err, text)
	}
	for _, action := range []string{"prepare", "init", "join"} {
		args := []string{"kubeadm", action, "--config", config, "--dry-run"}
		if action == "init" {
			args = append(args, "--advertise-address", "192.168.10.10")
		}
		if action == "join" {
			args = append(args, "--endpoint", "192.168.10.10:6443", "--token", "abcdef.0123456789abcdef", "--discovery-token-ca-cert-hash", "sha256:"+strings.Repeat("a", 64))
		}
		code, err, text = workflowRun(t, args...)
		if code != 0 || err != nil {
			t.Fatal(action, code, err, text)
		}
		if action == "join" && strings.Contains(text, "abcdef.0123456789abcdef") {
			t.Fatal("dry run leaked join token")
		}
		if action == "init" && (!strings.Contains(text, "kubernetesVersion: v1.24.7") || !strings.Contains(text, "cgroupDriver: systemd")) {
			t.Fatal(text)
		}
	}
}

func TestKubeadmInvalidInput(t *testing.T) {
	t.Chdir(t.TempDir())
	config := kubeadmTestConfig(t)
	for _, args := range [][]string{
		{"install", "--version", "latest"}, {"install", "--version", "v1.31.0"},
		{"install", "--version", "v1.26.15", "--cri-tools-version", "v1.24.2"}, {"install", "--kubeadm-download-base", "https://user:pass@example.com"},
		{"install", "--download-base", "https://old.example"}, {"init", "--apply"}, {"init"},
		{"join", "--endpoint", "192.168.10.10:6443", "--token", "abcdef.0123456789abcdef"},
		{"prepare", "--timeout", "0s"},
	} {
		full := append([]string{"kubeadm", args[0], "--config", config, "--dry-run"}, args[1:]...)
		code, err, _ := workflowRun(t, full...)
		if code == 0 || err == nil {
			t.Fatal("invalid arguments accepted", args)
		}
	}
}
func TestKubeadmStructuredConfiguration(t *testing.T) {
	var defaults fileConfig
	if err := yaml.Unmarshal(templates.ConfigTemplate, &defaults); err != nil {
		t.Fatal(err)
	}
	defaults.projectComponents()
	o := defaults.Kubeadm
	o.AdvertiseAddress = "192.168.10.10"
	o.NodeName = "control-1"
	o.ControlPlaneEndpoint = "api.example.com:6443"
	raw, err := renderKubeadm("init", "v1.24.7", o)
	if err != nil {
		t.Fatal(err)
	}
	var docs []map[string]any
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, doc)
	}
	if len(docs) != 3 || docs[0]["kind"] != "InitConfiguration" || docs[1]["controlPlaneEndpoint"] != "api.example.com:6443" || docs[2]["cgroupDriver"] != "systemd" {
		t.Fatal(string(raw))
	}
	dns, ok := docs[1]["dns"].(map[string]any)
	if !ok || dns["imageRepository"] != "registry.k8s.io/coredns" {
		t.Fatal("official CoreDNS namespace missing", string(raw))
	}
	custom := o
	custom.ImageRepository = "registry.example.com/custom"
	customRaw, err := renderKubeadm("init", "v1.24.7", custom)
	if err != nil || bytes.Contains(customRaw, []byte("registry.k8s.io/coredns")) {
		t.Fatal("custom repository overridden", err, string(customRaw))
	}
	for _, change := range []func(*kubeadmSettings){
		func(o *kubeadmSettings) { o.AdvertiseAddress = "127.0.0.1" },
		func(o *kubeadmSettings) { o.PodSubnet = o.ServiceSubnet },
		func(o *kubeadmSettings) { o.PodSubnet = "192.168.10.0/24" },
		func(o *kubeadmSettings) { o.CRISocket = "tcp://example.com:1234" },
		func(o *kubeadmSettings) { o.ControlPlaneEndpoint = "api.example.com" },
	} {
		bad := o
		change(&bad)
		if _, err := renderKubeadm("init", "v1.24.7", bad); err == nil {
			t.Fatal("invalid init configuration accepted")
		}
	}
}
func TestKubeadmContainerdCompatibility(t *testing.T) {
	for _, tc := range []struct {
		kube    int
		runtime string
		ok      bool
	}{
		{24, "containerd github.com/containerd/containerd v1.6.36", true},
		{24, "containerd v2.1.3", false}, {25, "containerd v2.0.4", false},
		{26, "containerd v1.6.14", false}, {26, "containerd v1.6.15", true},
		{26, "containerd v1.7.24", true}, {30, "containerd v2.1.3", true},
		{24, "containerd v1.5.0", false}, {24, "unknown", false},
	} {
		if err := checkKubeadmContainerd(tc.kube, tc.runtime); (err == nil) != tc.ok {
			t.Fatal(tc, err)
		}
	}
}
func TestKubeadmSwapFstab(t *testing.T) {
	source := "# /dev/old none swap sw 0 0\n/dev/sda1 / ext4 defaults 0 1\n/swap.img none swap sw 0 0\nUUID=x none swap defaults 0 0\n"
	result := disableFstabSwap(source)
	if strings.Count(result, "# opskit disabled swap:") != 2 || !strings.Contains(result, "/dev/sda1 / ext4 defaults 0 1") {
		t.Fatal(result)
	}
	if disableFstabSwap(result) != result {
		t.Fatal("fstab rewrite not idempotent")
	}
}
func TestKubeadmManifestRelativePath(t *testing.T) {
	config := kubeadmTestConfig(t)
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`    manifest: ""`), []byte(`    manifest: network.yaml`), 1)
	if err = os.WriteFile(config, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(config), "network.yaml"), []byte("apiVersion: v1\nkind: List\nitems: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	code, err, text := workflowRun(t, "kubeadm", "init", "--config", config, "--advertise-address", "192.168.10.10", "--dry-run")
	if code != 0 || err != nil || !strings.Contains(text, filepath.Join(filepath.Dir(config), "network.yaml")) {
		t.Fatal(code, err, text)
	}
}

func TestStageKubeadmTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux binary execution fixture uses POSIX scripts")
	}
	script := []byte("#!/bin/sh\ncase \"$0\" in\n *kubectl) echo '{\"clientVersion\":{\"gitVersion\":\"v1.24.7\"}}';;\n *crictl) echo 'crictl version v1.24.2';;\n *) echo 'v1.24.7';;\nesac\n")
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "crictl", Mode: 0755, Size: int64(len(script))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(script); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	var badChecksum atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := script
		if strings.Contains(r.URL.Path, "crictl") {
			data = archive.Bytes()
		}
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			if badChecksum.Load() {
				fmt.Fprint(w, strings.Repeat("0", 64))
				return
			}
			sum := sha256.Sum256(data)
			fmt.Fprintf(w, "%x\n", sum)
			return
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()
	bases := map[string]string{"kubeadm": server.URL, "kubelet": server.URL, "kubectl": server.URL}
	dir := t.TempDir()
	if err := stageKubeadmTools(context.Background(), "v1.24.7", "v1.24.2", bases, server.URL+"/crictl.tar.gz", t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	if err := verifyKubeadmTool(context.Background(), filepath.Join(dir, "kubeadm"), "kubeadm", "v1.24.8"); err == nil {
		t.Fatal("mismatched version accepted")
	}
	badChecksum.Store(true)
	if err := stageKubeadmTools(context.Background(), "v1.24.7", "v1.24.2", bases, server.URL+"/crictl.tar.gz", "", t.TempDir()); err == nil {
		t.Fatal("corrupt binary accepted")
	}
}

// Keep this test enabled on Windows: the target socket is a Linux path.
func TestKubeadmUnixSocketPathsAcrossManagementPlatforms(t *testing.T) {
	var defaults fileConfig
	if err := yaml.Unmarshal(templates.ConfigTemplate, &defaults); err != nil {
		t.Fatal(err)
	}
	defaults.projectComponents()
	o := defaults.Kubeadm
	o.AdvertiseAddress = "192.168.10.10"
	for _, socket := range []string{"unix:///run/containerd/containerd.sock", "unix:///run/cri-dockerd.sock", "unix:///var/run/containerd/containerd.sock"} {
		t.Run(socket, func(t *testing.T) {
			o.CRISocket = socket
			raw, err := renderKubeadm("init", "v1.24.7", o)
			if err != nil || !bytes.Contains(raw, []byte(socket)) {
				t.Fatalf("Linux socket rejected on %s: %s: %v\n%s", runtime.GOOS, socket, err, raw)
			}
		})
	}
	for _, socket := range []string{"unix:run/containerd.sock", "unix://localhost/run/containerd.sock", "unix://C:/run/containerd.sock", "tcp://127.0.0.1:1234", "unix:///run/containerd.sock?option=value", "unix:///run/containerd.sock#fragment"} {
		t.Run(socket, func(t *testing.T) {
			o.CRISocket = socket
			if _, err := renderKubeadm("init", "v1.24.7", o); err == nil {
				t.Fatalf("invalid socket accepted: %s", socket)
			}
		})
	}
}
