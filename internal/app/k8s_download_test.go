package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestK8sDownloadSelectionAndNoSSH(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "")
	config := filepath.Join(t.TempDir(), "minimal.yaml")
	if err := os.WriteFile(config, []byte("schema_version: 1\nk8s:\n  kubernetes: {version: v1.28.15}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "untouched")
	code, err, text := workflowRun(t, "k8s", "download", "--config", config, "--arch", "arm64", "--components", "kubeadm,etcd", "--etcd-version", "v3.5.5", "--download-dir", root, "--dry-run")
	if code != 0 || err != nil || !strings.Contains(text, "/v1.28.15/bin/linux/arm64/kubeadm") || !strings.Contains(text, "etcd-v3.5.5-linux-arm64.tar.gz") || strings.Contains(text, "crictl:") {
		t.Fatal(code, err, text)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("dry-run created files", err)
	}
	for _, args := range [][]string{
		{"--components", "etcd"}, {"--components", "unknown"}, {"--components", "kubectl,kubectl"},
		{"--components="}, {"--arch", "386"}, {"--apply"}, {"--download-base", "https://old.example"}, {"--timeout", "0s"},
	} {
		code, err, _ := workflowRun(t, append([]string{"k8s", "download", "--config", config, "--dry-run"}, args...)...)
		if code != 2 || err == nil {
			t.Fatal("invalid arguments accepted", args, code, err)
		}
	}
}

func TestK8sDownloadComponentSourceIsolation(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("KUBECTL_DOWNLOAD_BASE", "https://kubectl.example/release")
	t.Setenv("ETCD_DOWNLOAD_BASE", "https://etcd.example")
	config := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(config, []byte("schema_version: 1\ninstall:\n  kubeadm: {download_base: https://yaml.example/release}\n  kubectl: {download_base: https://yaml-kubectl.example/release}\n  etcd: {download_base: https://yaml-etcd.example}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	code, err, text := workflowRun(t, "k8s", "download", "--config", config, "--components", "kubeadm,kubectl,etcd", "--etcd-version", "v3.5.5", "--dry-run", "--arch", "amd64", "--kubectl-download-base", "https://explicit.example/release")
	if code != 0 || err != nil || !strings.Contains(text, "kubectl: https://explicit.example/release/") || !strings.Contains(text, "etcd: https://etcd.example/") || !strings.Contains(text, "kubeadm: https://yaml.example/release/") || strings.Contains(text, "yaml-etcd.example") || strings.Contains(text, "kubectl.example") {
		t.Fatal(code, err, text)
	}
}

func TestK8sDownloadFilesAndChecksumFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			payload := []byte("test component binary")
			sum := sha256.Sum256(payload)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1.24.7/bin/linux/arm64/kubelet.sha256":
					fmt.Fprintf(w, "%x", sum)
				case "/etcd-io/etcd/releases/download/v3.5.5/SHA256SUMS":
					fmt.Fprintf(w, "%s  unrelated.tar.gz\n%x  etcd-v3.5.5-linux-arm64.tar.gz\n", strings.Repeat("0", 64), sum)
				case "/v1.24.7/bin/linux/arm64/kubelet", "/etcd-io/etcd/releases/download/v3.5.5/etcd-v3.5.5-linux-arm64.tar.gz":
					if corrupt {
						fmt.Fprint(w, "corrupt")
					} else {
						w.Write(payload)
					}
				default:
					t.Errorf("unselected resource requested: %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			root := t.TempDir()
			code, err, text := workflowRun(t, "k8s", "download", "--components", "kubelet,etcd", "--etcd-version", "v3.5.5", "--arch", "arm64", "--download-dir", root, "--kubelet-download-base", server.URL, "--etcd-download-base", server.URL)
			entries, readErr := filepath.Glob(filepath.Join(root, "k8s-*"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if corrupt {
				if code == 0 || err == nil || len(entries) != 0 {
					t.Fatal("corrupt download published", code, err, entries)
				}
				return
			}
			if code != 0 || err != nil || len(entries) != 1 {
				t.Fatal(code, err, text, entries)
			}
			dir := entries[0]
			for _, name := range []string{"kubelet/kubelet", "kubelet/kubelet.sha256", "kubelet/kubelet.service", "kubelet/10-kubeadm.conf", "etcd/etcd-v3.5.5-linux-arm64.tar.gz", "etcd/SHA256SUMS"} {
				if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
					t.Fatal(name, err)
				}
			}
			raw, err := os.ReadFile(filepath.Join(dir, "downloads.json"))
			if err != nil {
				t.Fatal(err)
			}
			var records []componentDownloadRecord
			if err := json.Unmarshal(raw, &records); err != nil || len(records) != 4 {
				t.Fatal(err, string(raw))
			}
			for _, record := range records {
				m, err := inspect(filepath.Join(dir, record.File))
				if err != nil || m.SHA256 != record.SHA256 || (record.Source != "" && !record.UpstreamVerified) {
					t.Fatal(record, err)
				}
			}
			// Repeat without a reachable upstream; completed assets and checksum files are reused.
			server.Close()
			code, err, text = workflowRun(t, "k8s", "download", "--components", "kubelet,etcd", "--etcd-version", "v3.5.5", "--arch", "arm64", "--download-dir", root, "--kubelet-download-base", server.URL, "--etcd-download-base", server.URL)
			if code != 0 || err != nil {
				t.Fatal("repeat download contacted upstream", code, err, text)
			}
			assetURL := server.URL + "/v1.24.7/bin/linux/arm64/kubelet"
			if err := fetchClusterAssets(context.Background(), []clusterAsset{{Name: "kubelet", URL: assetURL, CheckURL: assetURL + ".sha256"}}, t.TempDir(), root, io.Discard); err != nil {
				t.Fatalf("create bundle failed to reuse download-only cache: %v", err)
			}
		})
	}
}

func TestK8sDownloadCalicoAndCancellation(t *testing.T) {
	var c fileConfig
	if err := yamlClusterDefaults(&c); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, calicoTestManifest) }))
	defer server.Close()
	sum := sha256.Sum256([]byte(calicoTestManifest))
	c.Install.Calico.DownloadBase = server.URL
	c.Install.Calico.SHA256 = fmt.Sprintf("%x", sum)
	root := t.TempDir()
	if err := saveClusterDownloads(context.Background(), c, nil, true, root, "amd64", io.Discard); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(root, "k8s-*", "calico", "network.yaml"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	raw, _ := os.ReadFile(files[0])
	if err := validateStandaloneCalicoManifest(raw, "v3.26.5", "10.244.0.0/16"); err != nil {
		t.Fatal(err)
	}
	c.K8s.Network.Manifest = files[0]
	if _, calico, err := selectedClusterAssets(c, "amd64", []string{"calico"}); err != nil || !calico {
		t.Fatal("rendered local manifest rejected", err)
	}
	c.K8s.Network.PodSubnet = "10.245.0.0/16"
	if _, _, err := selectedClusterAssets(c, "amd64", []string{"calico"}); err == nil {
		t.Fatal("mismatched local manifest accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root = t.TempDir()
	if err := saveClusterDownloads(ctx, c, nil, false, root, "amd64", io.Discard); err == nil {
		t.Fatal("cancelled task published")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal(entries)
	}
}
