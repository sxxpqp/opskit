package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const calicoTestManifest = `apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: calico-node
  namespace: kube-system
spec:
  template:
    spec:
      initContainers:
        - name: install-cni
          image: docker.io/calico/cni:v3.26.5
      containers:
        - name: calico-node
          image: docker.io/calico/node:v3.26.5
          env:
            - name: DATASTORE_TYPE
              value: kubernetes
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: calico-kube-controllers
  namespace: kube-system
spec:
  template:
    spec:
      containers:
        - name: calico-kube-controllers
          image: docker.io/calico/kube-controllers:v3.26.5
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: test.crd.projectcalico.org
spec:
  scope: Cluster
`

func TestClusterCalicoRenderAndValidation(t *testing.T) {
	raw, err := renderClusterCalico([]byte(calicoTestManifest), "v3.26.5", "10.244.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CALICO_IPV4POOL_CIDR", "10.244.0.0/16", "kubernetes-internal-ip", "DATASTORE_TYPE", "docker.io/calico/cni:v3.26.5", "test.crd.projectcalico.org"} {
		if !strings.Contains(string(raw), want) {
			t.Error("missing", want)
		}
	}
	// Re-rendering replaces settings without duplicating environment entries.
	again, err := renderClusterCalico(raw, "v3.26.5", "10.245.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(again), "name: CALICO_IPV4POOL_CIDR") != 1 || strings.Contains(string(again), "10.244.0.0/16") {
		t.Fatal(string(again))
	}
	for _, raw := range []string{"<html>bad gateway</html>", "apiVersion: v1\nkind: List\nitems: []", strings.Replace(calicoTestManifest, "calico/node:v3.26.5", "calico/node:latest", 1), strings.Replace(calicoTestManifest, "namespace: kube-system", "namespace: other", 1)} {
		if _, err := renderClusterCalico([]byte(raw), "v3.26.5", "10.244.0.0/16"); err == nil {
			t.Fatal("invalid manifest accepted", raw)
		}
	}
	c := clusterTestConfig(t, false)
	c.K8s.Network.Manifest = ""
	for _, minor := range []int{24, 25, 26, 27, 28} {
		if err := validateClusterCalico(&c.K8s, minor); err != nil {
			t.Fatal(err)
		}
	}
	for _, minor := range []int{23, 29, 30} {
		if err := validateClusterCalico(&c.K8s, minor); err == nil {
			t.Fatal("unsupported combination", minor)
		}
	}
	for _, cidr := range []string{"bad", "fd00::/64", "10.244.0.0/27", "10.244.1.1/16"} {
		c.K8s.Network.PodSubnet = cidr
		if err := validateClusterCalico(&c.K8s, 24); err == nil {
			t.Fatal("bad pool accepted", cidr)
		}
	}
}
func TestClusterCalicoDownloadAndChecksum(t *testing.T) {
	sum := sha256.Sum256([]byte(calicoTestManifest))
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/projectcalico/calico/v3.26.5/manifests/calico.yaml" {
			t.Errorf("path %s", r.URL.Path)
		}
		fmt.Fprint(w, calicoTestManifest)
	}))
	defer server.Close()
	c := clusterTestConfig(t, false)
	c.K8s.Network.Manifest = ""
	c.Install.Calico.DownloadBase = server.URL
	c.Install.Calico.SHA256 = hex.EncodeToString(sum[:])
	c.Install.DownloadDir = t.TempDir()
	work := t.TempDir()
	if err := prepareClusterNetwork(context.Background(), &c, work, io.Discard); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(c.K8s.Network.Manifest)
	if err != nil || !strings.Contains(string(raw), "10.244.0.0/16") || requests != 1 {
		t.Fatalf("%d %v %s", requests, err, raw)
	}
	c.K8s.Network.Manifest = ""
	c.Install.Calico.SHA256 = strings.Repeat("0", 64)
	work = t.TempDir()
	if err := prepareClusterNetwork(context.Background(), &c, work, io.Discard); err == nil {
		t.Fatal("checksum failure ignored")
	}
	if _, err := os.Stat(filepath.Join(work, "network.yaml")); !os.IsNotExist(err) {
		t.Fatal("bad download installed")
	}
	// Local override is copied exactly, and never fetched or altered.
	local := []byte("apiVersion: v1\nkind: List\nitems: []\n")
	c.K8s.Network.Manifest = filepath.Join(t.TempDir(), "custom.yaml")
	if err := os.WriteFile(c.K8s.Network.Manifest, local, 0600); err != nil {
		t.Fatal(err)
	}
	before := requests
	if err := prepareClusterNetwork(context.Background(), &c, t.TempDir(), io.Discard); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(c.K8s.Network.Manifest)
	if !bytes.Equal(got, local) || requests != before {
		t.Fatal("local override changed")
	}
}
func TestClusterCalicoDryRunDefaultsWithoutDownload(t *testing.T) {
	c := clusterTestConfig(t, false)
	c.K8s.Network.Manifest = ""
	c.K8s.Network.Calico.Version = ""
	c.Install.Calico.DownloadBase = ""
	c.Install.Calico.SHA256 = ""
	c.K8s.Hosts[0].Password = ""
	c.K8s.Hosts[0].PasswordEnv = "NOT_READ_DURING_DRY_RUN"
	t.Setenv("NOT_READ_DURING_DRY_RUN", "")
	t.Setenv("CALICO_DOWNLOAD_BASE", "http://127.0.0.1:1")
	raw, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code, err := Run(context.Background(), []string{"k8s", "create", "--config", path, "--dry-run"}, &out, io.Discard, "test")
	if code != 0 || err != nil || !strings.Contains(out.String(), "Calico v3.26.5") || !strings.Contains(out.String(), "http://127.0.0.1:1/projectcalico/") {
		t.Fatalf("%d %v %s", code, err, &out)
	}
}
func TestClusterCalicoWaitAndExistingConfig(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var calls []string
	err := waitClusterCalico(ctx, func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return []byte("ready\n"), nil
	}, io.Discard, "v3.26.5")
	if err != nil || len(calls) != 2 || !strings.Contains(calls[0], "daemonset/calico-node") || !strings.Contains(calls[1], "deployment/calico-kube-controllers") || !strings.Contains(calls[1], "--request-timeout=0") {
		t.Fatalf("%v %v", err, calls)
	}
	failure := errors.New("not ready")
	if err := waitClusterCalico(ctx, func(...string) ([]byte, error) { return nil, failure }, io.Discard, "v3.26.5"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := checkNewCalicoNetwork(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "10-other.conflist"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkNewCalicoNetwork(root); err == nil {
		t.Fatal("foreign CNI accepted")
	}
}

func TestClusterCalicoFallback(t *testing.T) {
	for _, badHash := range []bool{false, true} {
		t.Run(fmt.Sprintf("badHash=%t", badHash), func(t *testing.T) {
			fallbackCalls := 0
			fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fallbackCalls++
				_, _ = io.WriteString(w, calicoTestManifest)
			}))
			defer fallback.Close()
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if badHash {
					_, _ = io.WriteString(w, "corrupt")
					return
				}
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
			}))
			defer primary.Close()
			c := clusterTestConfig(t, false)
			c.K8s.Network.Manifest = ""
			c.Install.Calico.DownloadBase = primary.URL
			c.Install.Calico.FallbackBases = []string{fallback.URL}
			c.Install.DownloadDir = t.TempDir()
			sum := sha256.Sum256([]byte(calicoTestManifest))
			c.Install.Calico.SHA256 = hex.EncodeToString(sum[:])
			err := prepareClusterNetwork(context.Background(), &c, t.TempDir(), io.Discard)
			if badHash {
				if err == nil || !strings.Contains(err.Error(), "SHA-256") || fallbackCalls != 0 {
					t.Fatalf("bad hash bypassed: %v, calls=%d", err, fallbackCalls)
				}
			} else if err != nil || fallbackCalls != 1 {
				t.Fatalf("fallback failed: %v, calls=%d", err, fallbackCalls)
			}
		})
	}
}
