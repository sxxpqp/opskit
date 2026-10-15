package app

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("OPSKIT_FAKE_CHECK") != "" {
		fakeEtcdCheck()
		os.Exit(0)
	}
	if os.Getenv("OPSKIT_FAKE_ETCD") == "1" {
		if os.Getenv("OPSKIT_FAKE_FAIL") == "1" {
			fmt.Fprintln(os.Stderr, "sensitive-test-password=must-not-leak")
			os.Exit(7)
		}
		if err := os.WriteFile(os.Args[len(os.Args)-1], []byte("snapshot-content"), 0600); err != nil {
			os.Exit(8)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestBackupAndFailure(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPSKIT_FAKE_ETCD", "1")
	for _, fail := range []bool{false, true} {
		t.Setenv("OPSKIT_FAKE_FAIL", fmt.Sprint(map[bool]int{true: 1, false: 0}[fail]))
		root := t.TempDir()
		var out bytes.Buffer
		code, err := Run(context.Background(), []string{"etcd", "backup", "--etcdctl", exe, "--directory", root}, &out, &out, "test")
		files, _ := filepath.Glob(filepath.Join(root, "*", "manifest.json"))
		if fail {
			if code == 0 || err == nil || len(files) != 0 {
				t.Fatalf("failed export published: %d %v %v", code, err, files)
			}
		} else if code != 0 || err != nil || len(files) != 1 {
			t.Fatalf("backup: %d %v %s", code, err, out.String())
		}
	}
}
func TestUploadFailureRetainsBackup(t *testing.T) {
	exe, _ := os.Executable()
	t.Setenv("OPSKIT_FAKE_ETCD", "1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer server.Close()
	root := t.TempDir()
	var out bytes.Buffer
	code, err := Run(context.Background(), []string{"etcd", "backup", "--etcdctl", exe, "--directory", root, "--bucket", "test", "--s3-endpoint", server.URL, "--path-style"}, &out, &out, "test")
	files, _ := filepath.Glob(filepath.Join(root, "*", "snapshot.db"))
	if code != 5 || err == nil || len(files) != 1 {
		t.Fatalf("%d %v %v", code, err, files)
	}
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(files[0]), "manifest.json"))
	if !strings.Contains(string(b), `"upload_status": "failed"`) {
		t.Fatal(string(b))
	}
}
func TestS3ConditionalAndVerification(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	file := filepath.Join(t.TempDir(), "test")
	os.WriteFile(file, []byte("hello"), 0600)
	m, _ := inspect(file)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Authorization"), "Credential=config-access/") {
			t.Error("configured credentials did not override environment")
		}
		switch r.Method {
		case "PUT":
			if r.Header.Get("If-None-Match") != "*" {
				t.Error("missing conditional write")
			}
			w.Header().Set("ETag", `"test"`)
		case "HEAD":
			w.Header().Set("Content-Length", "5")
			w.Header().Set("X-Amz-Meta-Sha256", m.SHA256)
		default:
			t.Error(r.Method)
		}
	}))
	defer server.Close()
	s := storage{bucket: "test", key: "test", endpoint: server.URL, region: "us-east-1", pathStyle: true, accessKey: "config-access", secretKey: "config-secret"}
	if err := s.upload(context.Background(), file, m.SHA256); err != nil {
		t.Fatal(err)
	}
	if err := s.upload(context.Background(), file, "wrong"); err == nil {
		t.Fatal("verification mismatch accepted")
	}
}

func TestBackupFailureDoesNotLeakEtcdStderr(t *testing.T) {
	exe, err := os.Executable()
	if err != nil { t.Fatal(err) }
	t.Setenv("OPSKIT_FAKE_ETCD", "1")
	t.Setenv("OPSKIT_FAKE_FAIL", "1")
	var out bytes.Buffer
	code, runErr := Run(context.Background(), []string{"etcd", "backup", "--etcdctl", exe, "--directory", t.TempDir()}, &out, &out, "test")
	if code == 0 || runErr == nil { t.Fatalf("expected failure, got code=%d err=%v", code, runErr) }
	if strings.Contains(out.String()+runErr.Error(), "sensitive-test-password") { t.Fatal("etcdctl stderr leaked into diagnostics") }
}
