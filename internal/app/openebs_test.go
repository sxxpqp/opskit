package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const openEBSTestManifest = `apiVersion: v1
kind: Namespace
metadata: {name: openebs}
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: blockdevices.openebs.io}
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: blockdeviceclaims.openebs.io}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: openebs-ndm-operator, namespace: openebs}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: openebs-localpv-provisioner, namespace: openebs}
---
apiVersion: apps/v1
kind: DaemonSet
metadata: {name: openebs-ndm, namespace: openebs}
---
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata: {name: openebs-hostpath}
provisioner: openebs.io/local
volumeBindingMode: WaitForFirstConsumer
`

func TestOpenEBSDownloadFallbackAndIntegrity(t *testing.T) {
	var primaryCalls, fallbackCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls.Add(1)
		fmt.Fprint(w, "partial")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackCalls.Add(1)
		fmt.Fprint(w, openEBSTestManifest)
	}))
	defer fallback.Close()
	sum := sha256.Sum256([]byte(openEBSTestManifest))
	checksum := fmt.Sprintf("%x", sum)
	sources := []string{primary.URL, primary.URL, fallback.URL}
	raw, err := downloadOpenEBSOperator(context.Background(), sources, t.TempDir(), checksum, 50*time.Millisecond, io.Discard)
	if err != nil || string(raw) != openEBSTestManifest || primaryCalls.Load() != 1 || fallbackCalls.Load() != 1 {
		t.Fatal("timeout fallback, deduplication or partial response isolation failed", err, primaryCalls.Load(), fallbackCalls.Load())
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "invalid manifest bytes") }))
	defer bad.Close()
	if _, err := downloadOpenEBSOperator(context.Background(), []string{bad.URL, fallback.URL}, t.TempDir(), checksum, time.Second, io.Discard); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatal("digest mismatch accepted", err)
	}
	if fallbackCalls.Load() != 1 {
		t.Fatal("digest mismatch triggered fallback")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := downloadOpenEBSOperator(ctx, sources, t.TempDir(), checksum, time.Second, io.Discard); !errors.Is(err, context.Canceled) || primaryCalls.Load() != 1 || fallbackCalls.Load() != 1 {
		t.Fatal("parent cancellation started another request", err)
	}
}

func TestOpenEBSManifestAndPreflight(t *testing.T) {
	resources, err := validateOpenEBSManifest([]byte(openEBSTestManifest))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "<html>gateway</html>", strings.ReplaceAll(openEBSTestManifest, "namespace: openebs", "namespace: other"), openEBSTestManifest + "---\n" + openEBSTestManifest, strings.Replace(openEBSTestManifest, "WaitForFirstConsumer", "Immediate", 1), strings.Replace(openEBSTestManifest, "name: openebs-hostpath}", "name: other}", 1), strings.Replace(openEBSTestManifest, "metadata: {name: openebs-hostpath}", `metadata: {name: openebs-hostpath, annotations: {storageclass.kubernetes.io/is-default-class: "true"}}`, 1)} {
		if _, err := validateOpenEBSManifest([]byte(raw)); err == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
	for _, existing := range []bool{false, true} {
		kube := func(args ...string) ([]byte, error) {
			if args[0] != "get" {
				t.Fatal("preflight mutated cluster", args)
			}
			if existing && strings.Contains(strings.Join(args, " "), "deployment/openebs-ndm-operator") {
				return []byte("deployment/openebs-ndm-operator"), nil
			}
			return nil, nil
		}
		if err := preflightOpenEBS(kube, resources, false); (err != nil) != existing {
			t.Fatal(existing, err)
		}
	}
	denied := errors.New("forbidden")
	if err := preflightOpenEBS(func(...string) ([]byte, error) { return nil, denied }, resources, false); !errors.Is(err, denied) {
		t.Fatal("read denial ignored", err)
	}
}

func TestOpenEBSDefaultAndWait(t *testing.T) {
	for _, tc := range []struct {
		name, annotation string
		rejected         bool
	}{
		{"local-path", "storageclass.kubernetes.io/is-default-class", true},
		{"local-path", "storageclass.beta.kubernetes.io/is-default-class", true},
		{"openebs-hostpath", "storageclass.kubernetes.io/is-default-class", false},
	} {
		err := checkOpenEBSDefault(func(...string) ([]byte, error) {
			return []byte(fmt.Sprintf(`{"items":[{"metadata":{"name":%q,"annotations":{%q:"true"}}}]}`, tc.name, tc.annotation)), nil
		})
		if (err != nil) != tc.rejected {
			t.Fatal(tc, err)
		}
	}
	resources, _ := validateOpenEBSManifest([]byte(openEBSTestManifest))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	waits := 0
	if err := waitOpenEBS(ctx, func(args ...string) ([]byte, error) {
		waits++
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--timeout=") || !strings.Contains(joined, "--request-timeout=0") {
			t.Fatal("wait does not use total deadline", joined)
		}
		return nil, nil
	}, resources, io.Discard); err != nil || waits != 5 {
		t.Fatal(err, waits)
	}
	cancel()
	if err := waitOpenEBS(ctx, func(...string) ([]byte, error) { t.Fatal("called after cancellation"); return nil, nil }, resources, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestOpenEBSDryRunDownloadAndApply(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "")
	if code, err, _ := workflowRun(t, "openebs", "install", "--dry-run"); code != 0 || err != nil {
		t.Fatal(code, err)
	}
	for _, flags := range [][]string{{"--operator-url", "file:///tmp/operator.yaml"}, {"--sha256", "bad"}, {"--timeout", "0s"}, {"--apply"}} {
		if code, err, _ := workflowRun(t, append([]string{"openebs", "install", "--dry-run"}, flags...)...); code != 2 || err == nil {
			t.Fatal("invalid flags accepted", flags, code, err)
		}
	}
	dir := fakeTools(t)
	t.Setenv("OPSKIT_FAKE_CURRENT_CONTEXT", "openebs-test")
	manifest := filepath.Join(t.TempDir(), "operator.yaml")
	if err := os.WriteFile(manifest, []byte(openEBSTestManifest), 0600); err != nil {
		t.Fatal(err)
	}
	code, err, text := workflowRun(t, "openebs", "install", "--manifest", manifest, "--set-default", "--kubeconfig", "explicit.conf")
	if code != 0 || err != nil || !strings.Contains(text, "已就绪") {
		t.Fatal(code, err, text)
	}
	log, err := os.ReadFile(filepath.Join(dir, "events"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "patch storageclass openebs-hostpath") || !strings.Contains(string(log), "--context openebs-test") {
		t.Fatal("default class or fixed context missing", string(log))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, openEBSTestManifest) }))
	defer server.Close()
	config := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(config, []byte("schema_version: 1\ninstall:\n  download_dir: cache\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(openEBSTestManifest))
	args := []string{"openebs", "install", "--config", config, "--operator-url", server.URL + "/operator.yaml", "--sha256", fmt.Sprintf("%x", sum), "--wait=false"}
	if err := os.WriteFile(filepath.Join(dir, "events"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if code, err, text := workflowRun(t, args...); code != 0 || err != nil || !strings.Contains(text, "未等待") {
		t.Fatal(code, err, text)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "events"))
	if strings.Contains(string(before), "patch storageclass") || strings.Contains(string(before), "rollout status") {
		t.Fatal("default installation patched default class or ignored --wait=false", string(before))
	}
	args[len(args)-2] = strings.Repeat("0", 64)
	if code, err, _ := workflowRun(t, args...); code == 0 || err == nil {
		t.Fatal("checksum mismatch ignored", code, err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "events"))
	if string(before) != string(after) {
		t.Fatal("checksum mismatch contacted cluster")
	}
}

func TestOpenEBSConfigPrecedenceAndHelp(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "")
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "operator.yaml"), []byte(openEBSTestManifest), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(config, []byte("schema_version: 1\nopenebs:\n  manifest: operator.yaml\n  set_default: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	code, err, text := workflowRun(t, "openebs", "install", "--config", config, "--dry-run", "--set-default=false")
	if code != 0 || err != nil || !strings.Contains(text, filepath.Join(configDir, "operator.yaml")) || !strings.Contains(text, "默认存储类=false") {
		t.Fatal("relative path or explicit bool override broken", code, err, text)
	}
	if err := os.WriteFile(config, []byte("schema_version: 1\ninstall:\n  openebs:\n    operator_url: https://config.example/operator.yaml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENEBS_OPERATOR_URL", "https://env.example/operator.yaml")
	code, err, text = workflowRun(t, "openebs", "install", "--config", config, "--dry-run")
	if code != 0 || err != nil || !strings.Contains(text, "https://env.example/") {
		t.Fatal("environment override broken", code, err, text)
	}
	code, err, text = workflowRun(t, "openebs", "install", "--config", config, "--dry-run", "--operator-url", "https://cli.example/operator.yaml")
	if code != 0 || err != nil || !strings.Contains(text, "https://cli.example/") {
		t.Fatal("CLI override broken", code, err, text)
	}
	if err := os.WriteFile(config, []byte("broken: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if code, err, text := workflowRun(t, "openebs", "install", "--config", config, "--help"); code != 0 || err != nil || !strings.Contains(text, "--operator-url") {
		t.Fatal("help read config or external tools", code, err, text)
	}
}
