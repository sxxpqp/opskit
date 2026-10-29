package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestS3ReadIntegrityAndNoOverwrite(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	body := []byte("snapshot-content")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/test/bad" {
			w.Header().Set("X-Amz-Meta-Sha256", strings.Repeat("0", 64))
		} else {
			w.Header().Set("X-Amz-Meta-Sha256", digest)
		}
		w.Write(body)
	}))
	defer server.Close()
	base := []string{"--bucket", "test", "--s3-endpoint", server.URL, "--region", "us-east-1", "--path-style", "--key", "good"}
	dest := filepath.Join(t.TempDir(), "snapshot.db")
	code, err, output := workflowRun(t, append([]string{"s3", "download", "--file", dest}, base...)...)
	if code != 0 || err != nil || !strings.Contains(output, "verified-content") {
		t.Fatal(code, err, output)
	}
	actual, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(actual, body) {
		t.Fatal(err, string(actual))
	}
	code, err, _ = workflowRun(t, append([]string{"s3", "download", "--file", dest}, base...)...)
	if code == 0 || err == nil {
		t.Fatal("download replaced existing file")
	}
	base[len(base)-1] = "bad"
	missing := filepath.Join(t.TempDir(), "bad.db")
	code, err, _ = workflowRun(t, append([]string{"s3", "download", "--file", missing}, base...)...)
	if code == 0 || err == nil {
		t.Fatal("wrong digest accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("unverified file published")
	}
	code, err, _ = workflowRun(t, append([]string{"s3", "verify", "--sha256", digest}, base...)...)
	if code != 0 || err != nil {
		t.Fatal("explicit digest did not override metadata", code, err)
	}
}

func TestS3ListPagination(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("continuation-token") == "next" {
			fmt.Fprint(w, `<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>second</Key><Size>2</Size></Contents></ListBucketResult>`)
		} else {
			fmt.Fprint(w, `<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>next</NextContinuationToken><Contents><Key>first</Key><Size>1</Size></Contents></ListBucketResult>`)
		}
	}))
	defer server.Close()
	code, err, output := workflowRun(t, "s3", "list", "--bucket", "test", "--s3-endpoint", server.URL, "--path-style", "--limit", "2")
	if code != 0 || err != nil || requests != 2 || !strings.Contains(output, "second") {
		t.Fatal(code, err, requests, output)
	}
}

func createBackupFixture(t *testing.T, root, name string, created time.Time, status string) string {
	t.Helper()
	directory := filepath.Join(root, name)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "snapshot.db")
	if err := os.WriteFile(file, []byte(name), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := inspect(file)
	if err != nil {
		t.Fatal(err)
	}
	m.Created, m.Upload = created, status
	if err := writeManifest(filepath.Join(directory, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestBackupPruneProtectsIncompleteAndLatest(t *testing.T) {
	t.Chdir(t.TempDir())
	root := t.TempDir()
	old := time.Now().Add(-90 * 24 * time.Hour)
	removable := createBackupFixture(t, root, "old", old, "not-requested")
	latest := createBackupFixture(t, root, "latest", time.Now(), "not-requested")
	failed := createBackupFixture(t, root, "failed", old, "failed")
	corrupt := createBackupFixture(t, root, "corrupt", old, "not-requested")
	if err := os.WriteFile(filepath.Join(corrupt, "snapshot.db"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	code, err, output := workflowRun(t, "backup", "prune", "--directory", root, "--keep", "1", "--older-than", "24h")
	if code != 0 || err != nil || !strings.Contains(output, `"selected":["old"]`) {
		t.Fatal(code, err, output)
	}
	if _, err := os.Stat(removable); err != nil {
		t.Fatal("preview deleted backup", err)
	}
	code, err, _ = workflowRun(t, "backup", "prune", "--directory", root, "--keep", "1", "--older-than", "24h", "--dry-run=false")
	if code != 0 || err != nil {
		t.Fatal(code, err)
	}
	if _, err := os.Stat(removable); !os.IsNotExist(err) {
		t.Fatal("selected backup retained", err)
	}
	for _, path := range []string{latest, failed, corrupt} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("protected backup deleted", path, err)
		}
	}
	code, err, _ = workflowRun(t, "backup", "verify", "--directory", corrupt)
	if code == 0 || err == nil {
		t.Fatal("corrupt backup verified")
	}
}

func TestBundleRoundTripAndRejectTraversal(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	os.Mkdir(source, 0700)
	file := filepath.Join(source, "kubectl")
	os.WriteFile(file, []byte("binary-fixture"), 0600)
	m, err := inspect(file)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]componentDownloadRecord{{Component: "kubectl", File: "kubectl", SHA256: m.SHA256}})
	os.WriteFile(filepath.Join(source, "downloads.json"), raw, 0600)
	os.WriteFile(filepath.Join(source, "config.yaml"), []byte("personal-credentials-do-not-export"), 0600)
	archive := filepath.Join(root, "bundle.tar.gz")
	if err := exportBundle(ctx, source, archive, nil, nil); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "imported")
	if err := importBundle(ctx, archive, dest, 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "components", "config.yaml")); !os.IsNotExist(err) {
		t.Fatal("unrelated configuration was exported")
	}
	if err := importBundle(ctx, archive, filepath.Join(root, "too-large"), 1); err == nil {
		t.Fatal("uncompressed size limit ignored")
	}
	if err := importBundle(ctx, archive, dest, 1<<20); err == nil {
		t.Fatal("existing destination overwritten")
	}
	os.WriteFile(filepath.Join(dest, "components", "kubectl"), []byte("tampered"), 0600)
	if _, err := verifyBundle(ctx, dest); err == nil {
		t.Fatal("corruption accepted")
	}
	bad := filepath.Join(root, "bad.tar.gz")
	f, _ := os.Create(bad)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "../escape", Typeflag: tar.TypeReg, Mode: 0600, Size: 1})
	tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	f.Close()
	if err := importBundle(ctx, bad, filepath.Join(root, "unsafe"), 1<<20); err == nil {
		t.Fatal("path traversal accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
		t.Fatal("archive escaped destination")
	}
}

func TestDoctorAndStructuredReports(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	code, err, output := workflowRun(t, "doctor", "--profile", "kind", "--format", "json")
	if code != 3 || err == nil || !json.Valid([]byte(output)) || !strings.Contains(output, "opskit kind install") {
		t.Fatal(code, err, output)
	}
	code, err, output = workflowRun(t, "version", "--output", "json")
	if code != 0 || err != nil || !json.Valid([]byte(output)) || !strings.Contains(output, `"exit_code":0`) {
		t.Fatal(code, err, output)
	}
	code, err, output = workflowRun(t, "kind", "create", "--output", "json")
	if code != 3 || err == nil || !json.Valid([]byte(output)) || !strings.Contains(output, "dependency-unavailable") {
		t.Fatal(code, err, output)
	}
}

func TestDoctorNetworkAndRetentionConfiguration(t *testing.T) {
	t.Chdir(t.TempDir())
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodHead {
			t.Error("diagnosis downloaded content")
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	root := t.TempDir()
	config := filepath.Join(root, "config.yaml")
	body := fmt.Sprintf("schema_version: 1\ninstall:\n  download_dir: %s\n  kubectl: {download_base: %s}\n  kind: {download_base: %s}\n  containerd: {download_base: %s}\n  go: {download_base: %s}\nbackup:\n  retention: {keep: 2, older_than: 24h}\n", filepath.ToSlash(root), server.URL, server.URL, server.URL, server.URL)
	if err := os.WriteFile(config, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	code, err, output := workflowRun(t, "doctor", "--config", config, "--network", "--format", "json")
	if code != 0 || err != nil || requests != 4 {
		t.Fatal(code, err, requests, output)
	}
	backups := t.TempDir()
	createBackupFixture(t, backups, "old", time.Now().Add(-72*time.Hour), "not-requested")
	createBackupFixture(t, backups, "new", time.Now(), "not-requested")
	code, err, output = workflowRun(t, "backup", "prune", "--config", config, "--directory", backups)
	if code != 0 || err != nil || !strings.Contains(output, `"selected":[]`) {
		t.Fatal(code, err, output)
	}
	code, err, output = workflowRun(t, "backup", "prune", "--config", config, "--directory", backups, "--keep", "1")
	if code != 0 || err != nil || !strings.Contains(output, `"selected":["old"]`) {
		t.Fatal(code, err, output)
	}
}
