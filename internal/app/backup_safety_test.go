package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBackupDoesNotLeakEtcdStdout(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		fail string
		code int
	}{
		{name: "success", fail: "0", code: 0},
		{name: "failure", fail: "1", code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OPSKIT_FAKE_ETCD", "1")
			t.Setenv("OPSKIT_FAKE_STDOUT", "1")
			t.Setenv("OPSKIT_FAKE_FAIL", tc.fail)

			var out, diag bytes.Buffer
			code, runErr := Run(context.Background(), []string{
				"etcd", "backup", "--etcdctl", exe,
				"--directory", t.TempDir(),
			}, &out, &diag, "test")
			if code != tc.code {
				t.Fatalf("unexpected code: %d (want %d), err=%v", code, tc.code, runErr)
			}
			if (tc.code == 0) != (runErr == nil) {
				t.Fatalf("unexpected result: code=%d err=%v", code, runErr)
			}
			all := out.String() + diag.String()
			if runErr != nil {
				all += runErr.Error()
			}
			if strings.Contains(all, "sensitive-test-stdout") ||
				strings.Contains(all, "sensitive-test-password") {
				t.Fatal("raw etcdctl output leaked into public diagnostics")
			}
		})
	}
}

func TestWriteManifestUpdatesAtomicallyWithPrivateMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	m := Manifest{SchemaVersion: 1, File: "snapshot.db", Size: 12, SHA256: "test-sha256", Upload: "pending"}
	if err := writeManifest(path, m); err != nil {
		t.Fatal(err)
	}

	m.Upload = "verified-metadata"
	if err := writeManifest(path, m); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Manifest
	if err := json.Unmarshal(content, &got); err != nil {
		t.Fatalf("written manifest is invalid JSON: %v", err)
	}
	if got.Upload != m.Upload || got.Size != m.Size || got.SHA256 != m.SHA256 {
		t.Fatalf("manifest data changed unexpectedly: %+v", got)
	}
	temps, err := filepath.Glob(filepath.Join(dir, ".manifest-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 0 {
		t.Fatalf("left over temp files: %v", temps)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("manifest permissions %o, want 0600", info.Mode().Perm())
		}
	}
}
