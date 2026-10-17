package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	t.Setenv("HOME", root)
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("ZDOTDIR", "")
	t.Setenv("PATH", "/usr/bin:/bin")
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

func TestDefaultDownloadDirectory(t *testing.T) {
	home := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}
	got, err := defaultDownloadDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".opskit", "downloads")
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}
func TestKindLinkCandidates(t *testing.T) {
	home := t.TempDir()
	local := filepath.Join(home, "Local")
	cases := []struct {
		platform string
		root     bool
		want     []string
	}{
		{"linux", true, []string{"/usr/local/bin"}},
		{"linux", false, []string{filepath.Join(home, ".local", "bin")}},
		{"darwin", false, []string{"/opt/homebrew/bin", "/usr/local/bin", filepath.Join(home, ".local", "bin")}},
		{"windows", false, []string{filepath.Join(local, "opskit", "bin")}},
	}
	for _, c := range cases {
		got := kindLinkCandidates(c.platform, home, local, c.root)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Fatalf("%s: %v", c.platform, got)
		}
	}
	got := kindLinkCandidates("windows", home, "", false)
	if got[0] != filepath.Join(home, "AppData", "Local", "opskit", "bin") {
		t.Fatal(got)
	}
}
func TestKindPathProfiles(t *testing.T) {
	home := t.TempDir()
	dot := filepath.Join(home, "dotfiles")
	got := kindPathProfiles(home, "/bin/zsh", dot)
	if len(got) != 1 || got[0] != filepath.Join(dot, ".zshrc") {
		t.Fatal(got)
	}
	if e := os.WriteFile(filepath.Join(home, ".bash_profile"), []byte("# existing\n"), 0600); e != nil {
		t.Fatal(e)
	}
	got = kindPathProfiles(home, "/bin/bash", "")
	if len(got) != 2 || got[1] != filepath.Join(home, ".bash_profile") {
		t.Fatal(got)
	}
	if len(kindPathProfiles(home, "/usr/bin/fish", "")) != 0 {
		t.Fatal("unknown shell should not receive POSIX syntax")
	}
}
func TestPersistUnixKindPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell profiles")
	}
	home := t.TempDir()
	dir := filepath.Join(home, "my tools' bin")
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("PATH", "/usr/bin:/bin")
	initial := "# existing startup configuration\nexport KEEP_ME=yes\n"
	profile := filepath.Join(home, ".bashrc")
	if e := os.WriteFile(profile, []byte(initial), 0640); e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e := finishKindPath(dir, &out); e != nil {
		t.Fatal(e)
	}
	first, e := os.ReadFile(profile)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(string(first), initial) || !strings.Contains(string(first), shellPathLine(dir)) {
		t.Fatal(string(first))
	}
	cmd := exec.Command("/bin/sh", "-c", `. "$1"; printf '%s' "$PATH"`, "sh", profile)
	cmd.Env = append(os.Environ(), "PATH=/usr/bin:/bin")
	result, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("profile shell syntax: %v %s", e, result)
	}
	if string(result) != dir+":/usr/bin:/bin" {
		t.Fatalf("profile PATH: %q", result)
	}
	backups, _ := filepath.Glob(profile + ".backup-*")
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	if e := finishKindPath(dir, &out); e != nil {
		t.Fatal(e)
	}
	second, _ := os.ReadFile(profile)
	if !bytes.Equal(first, second) {
		t.Fatal("repeated PATH registration changed profile")
	}
	if _, e := os.Stat(filepath.Join(home, ".profile")); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin")
	out.Reset()
	if e := finishKindPath(dir, &out); e != nil {
		t.Fatal(e)
	}
	if out.Len() != 0 {
		t.Fatal("PATH already present should not touch profiles")
	}
}
