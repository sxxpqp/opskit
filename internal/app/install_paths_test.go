package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDownloadRetained(t *testing.T) {
	root := t.TempDir()
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "failed", 500)
			return
		}
		io.WriteString(w, "package-data")
	}))
	defer server.Close()
	var dst bytes.Buffer
	if e := downloadRetained(context.Background(), server.URL+"/kind-linux-amd64", &dst, root, "kind", "v0.17.0"); e != nil {
		t.Fatal(e)
	}
	files, _ := filepath.Glob(filepath.Join(root, "kind", "v0.17.0", "*", "kind-linux-amd64"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	b, e := os.ReadFile(files[0])
	if e != nil || string(b) != dst.String() {
		t.Fatalf("%s %v", b, e)
	}
	fail = true
	if e := downloadRetained(context.Background(), server.URL+"/kind-linux-amd64", io.Discard, root, "kind", "v0.17.0"); e == nil {
		t.Fatal("accepted HTTP failure")
	}
	b, _ = os.ReadFile(files[0])
	if string(b) != "package-data" {
		t.Fatal("prior download changed")
	}
	partial, _ := filepath.Glob(filepath.Join(root, "kind", "v0.17.0", "*", ".download-*"))
	if len(partial) != 0 {
		t.Fatal(partial)
	}
}
func TestExposeKind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior is Unix-specific")
	}
	root := t.TempDir()
	target := filepath.Join(root, "versions", "v0.17.0", "kind")
	if e := os.MkdirAll(filepath.Dir(target), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(target, []byte("kind"), 0755); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(root, "bin")
	var out bytes.Buffer
	if e := exposeKind(target, dir, &out); e != nil {
		t.Fatal(e)
	}
	if e := exposeKind(target, dir, &out); e != nil {
		t.Fatalf("repeated install: %v", e)
	}
	link, e := os.Readlink(filepath.Join(dir, "kind"))
	if e != nil || link != target {
		t.Fatalf("%s %v", link, e)
	}
	if !strings.Contains(out.String(), "export PATH=") {
		t.Fatal(out.String())
	}
	other := filepath.Join(root, "other")
	if e := os.MkdirAll(other, 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(other, "kind"), []byte("other-install"), 0755); e != nil {
		t.Fatal(e)
	}
	if e := exposeKind(target, other, io.Discard); e == nil {
		t.Fatal("overwrote unrelated kind")
	}
	b, _ := os.ReadFile(filepath.Join(other, "kind"))
	if string(b) != "other-install" {
		t.Fatal(string(b))
	}
}
