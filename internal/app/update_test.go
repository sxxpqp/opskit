package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/sxxpqp/opskit/internal/selfupdate"
)

func TestUpdateCheckAndDownload(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ZDOTDIR", "")
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	key := base64.StdEncoding.EncodeToString(public)
	exe, _ := os.Executable()
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	m := selfupdate.Manifest{SchemaVersion: 1, Version: "v1.2.3", BuildVersion: "v1.2.3-abcdef0", Assets: map[string]selfupdate.Asset{}}
	for _, platform := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name, _ := selfupdate.AssetName(platform, arch)
			m.Assets[name] = selfupdate.Asset{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}
		}
	}
	signed, _ := selfupdate.Sign(m, private)
	requests := []string{}
	var requestMu sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMu.Lock()
		requests = append(requests, r.URL.Path)
		requestMu.Unlock()
		if strings.HasSuffix(r.URL.Path, ".json") {
			w.Write(signed)
			return
		}
		w.Write(data)
	}))
	defer server.Close()
	transport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = transport }()
	run := func(current string, extra ...string) (int, error, string) {
		var out bytes.Buffer
		args := append([]string{"update", "--update-download-base", server.URL, "--public-key", key}, extra...)
		code, err := Run(context.Background(), args, &out, &out, current)
		return code, err, out.String()
	}
	if code, err, out := run("v1.0.0-old", "--check"); code != 0 || err != nil || !strings.Contains(out, "有可用更新") {
		t.Fatalf("%d %v %s", code, err, out)
	}
	requestMu.Lock()
	if len(requests) != 1 || requests[0] != "/channels/stable.json" {
		t.Fatal(requests)
	}
	requestMu.Unlock()
	if code, err, _ := run("v1.2.3-old", "--check"); code != 0 || err != nil {
		t.Fatal(code, err)
	}
	if code, err, _ := run("v2.0.0", "--check"); code == 0 || err == nil {
		t.Fatal("downgrade accepted")
	}
	if code, err, _ := run("v1.0.0", "--check", "--version", "v1.2.4"); code == 0 || err == nil {
		t.Fatal("mismatched version accepted")
	}
	t.Setenv("OPSKIT_FAKE_SELFUPDATE_VERSION", m.BuildVersion)
	dir := t.TempDir()
	if code, err, out := run("v1.0.0", "--download-only", "--download-dir", dir); code != 0 || err != nil {
		t.Fatalf("%d %v %s", code, err, out)
	}
	name, _ := selfupdate.AssetName(runtime.GOOS, runtime.GOARCH)
	file, err := os.Lstat(filepath.Join(dir, name))
	if err != nil || !file.Mode().IsRegular() {
		t.Fatalf("%v %v", file, err)
	}
	if runtime.GOOS != "windows" && file.Mode().Perm() != 0755 {
		t.Fatal(file.Mode())
	}
	if code, err, _ := run("v1.0.0", "--download-only", "--download-dir", dir); code == 0 || err == nil {
		t.Fatal("download overwrote an existing file")
	}
	if runtime.GOOS != "windows" {
		install := t.TempDir()
		if code, err, out := run("v1.0.0", "--install-dir", install); code != 0 || err != nil {
			t.Fatalf("%d %v %s", code, err, out)
		}
		info, err := os.Lstat(filepath.Join(install, "opskit"))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0755 {
			t.Fatalf("%v %v", info, err)
		}
	}
}

func TestUpdateReplacementRestoresOriginal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("automatic replacement supported on Unix")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "opskit")
	stage := filepath.Join(dir, "stage")
	if err := os.WriteFile(target, []byte("original"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stage, []byte("new"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := replaceUpdatedBinary(stage, target, func(string) error { return errors.New("version failed") }); err == nil {
		t.Fatal("invalid version installed")
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "original" {
		t.Fatalf("original lost: %q %v", b, err)
	}
	if err := os.WriteFile(stage, []byte("new"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := replaceUpdatedBinary(stage, target, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "opskit.backup-*"))
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	b, _ = os.ReadFile(backups[0])
	if string(b) != "original" {
		t.Fatal("backup changed")
	}
}
