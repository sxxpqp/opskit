package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func TestVerifyKindCommand(t *testing.T) {
	dir := fakeTools(t)
	var out bytes.Buffer
	if err := verifyKindCommand(context.Background(), dir, "v0.17.0", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "PATH 验证通过") {
		t.Fatal(out.String())
	}
	if err := verifyKindCommand(context.Background(), dir, "v0.33.0", io.Discard); err == nil {
		t.Fatal("wrong version was accepted")
	}
	binary := "kind"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	shadow := t.TempDir()
	b, err := os.ReadFile(filepath.Join(dir, binary))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(shadow, binary), b, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shadow+string(os.PathListSeparator)+dir)
	if err = verifyKindCommand(context.Background(), dir, "v0.17.0", io.Discard); err == nil || !strings.Contains(err.Error(), "优先命中") {
		t.Fatalf("shadowed kind accepted: %v", err)
	}
	t.Setenv("PATH", shadow)
	out.Reset()
	if err = verifyKindCommand(context.Background(), dir, "v0.17.0", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "PATH 待生效") || strings.Contains(out.String(), "PATH 验证通过") {
		t.Fatal(out.String())
	}
}
func TestUnsupportedShellPathFailsClearly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/usr/bin/fish")
	t.Setenv("PATH", "/usr/bin:/bin")
	err := finishKindPath(filepath.Join(t.TempDir(), "bin"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "不支持自动 PATH") {
		t.Fatalf("unsupported shell accepted: %v", err)
	}
}

func TestKindInFreshUnixShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix startup files")
	}
	dir := fakeTools(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Launch a fresh interactive shell with an initial PATH that excludes the tool.
	line := shellPathLine(dir) + "\n"
	cases := []struct {
		shell   string
		args    []string
		profile string
	}{
		{"/bin/bash", []string{"--noprofile", "--rcfile", filepath.Join(home, ".bashrc"), "-ic", "command -v kind; kind version"}, ".bashrc"},
		{"/bin/zsh", []string{"-ic", "command -v kind; kind version"}, ".zshrc"},
	}
	for _, c := range cases {
		t.Run(c.shell, func(t *testing.T) {
			if _, err := os.Stat(c.shell); err != nil {
				t.Skip("shell unavailable")
			}
			if err := os.WriteFile(filepath.Join(home, c.profile), []byte(line), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, c.shell, c.args...)
			cmd.Env = append(os.Environ(), "HOME="+home, "ZDOTDIR="+home, "PATH=/usr/bin:/bin")
			b, err := cmd.Output()
			if err != nil {
				t.Fatalf("new shell: %v %s", err, b)
			}
			if !strings.Contains(string(b), filepath.Join(dir, "kind")) || !strings.Contains(string(b), "kind v0.17.0") {
				t.Fatalf("new shell resolved wrong kind: %s", b)
			}
		})
	}
}
func TestWindowsUserPathReadBack(t *testing.T) {
	if runtime.GOOS != "windows" || os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("isolated Windows GitHub runner registry test")
	}
	// The hosted runner is ephemeral. Restore its exact prior User PATH even if the assertion fails.
	script := `$ErrorActionPreference='Stop'
$before=[Environment]::GetEnvironmentVariable('Path','User')
try {
& {` + windowsKindPathScript + `}
& {` + windowsKindPathScript + `}
$saved=[Environment]::GetEnvironmentVariable('Path','User')
$matchingEntries=@($saved -split ';' | Where-Object { [string]::Equals($_.TrimEnd('\'),$env:OPSKIT_KIND_LINK_DIR.TrimEnd('\'),[StringComparison]::OrdinalIgnoreCase) })
if ($matchingEntries.Count -ne 1) { throw 'PATH registration was not idempotent' }
} finally { [Environment]::SetEnvironmentVariable('Path',$before,'User') }
`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "OPSKIT_KIND_LINK_DIR="+filepath.Join(t.TempDir(), "tools space's"))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Windows user PATH: %v %s", err, b)
	}
}

func TestChooseKindLinkDir(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "path tools")
	preferred := filepath.Join(root, "preferred")
	fallback := filepath.Join(root, "fallback")
	for _, d := range []string{existing, preferred} {
		if err := os.Mkdir(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	sep := string(os.PathListSeparator)
	paths := strings.Join([]string{"", "relative-bin", file, filepath.Join(root, "missing"), existing}, sep)
	got, err := chooseKindLinkDir(paths, []string{preferred, fallback})
	if err != nil || got != existing {
		t.Fatalf("existing custom PATH directory not reused: %s %v", got, err)
	}
	got, err = chooseKindLinkDir(existing+sep+preferred, []string{preferred, fallback})
	if err != nil || got != preferred {
		t.Fatalf("preferred PATH directory not selected: %s %v", got, err)
	}
	got, err = chooseKindLinkDir(strings.Join([]string{"", "relative-bin", file, filepath.Join(root, "missing")}, sep), []string{preferred, fallback})
	if err != nil || got != fallback {
		t.Fatalf("fallback: %s %v", got, err)
	}
	// Choosing a directory must not create the fallback or leave probe files.
	if _, err = os.Stat(fallback); !os.IsNotExist(err) {
		t.Fatalf("fallback created prematurely: %v", err)
	}
	for _, d := range []string{existing, preferred} {
		files, err := os.ReadDir(d)
		if err != nil || len(files) != 0 {
			t.Fatalf("probe files left in %s: %v %v", d, files, err)
		}
	}
}
func TestChooseKindLinkDirSkipsReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("Unix non-root permission check")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	writable := filepath.Join(root, "writable")
	for _, d := range []string{locked, writable} {
		if err := os.Mkdir(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(locked, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0755) })
	got, err := chooseKindLinkDir(locked+string(os.PathListSeparator)+writable, []string{filepath.Join(root, "fallback")})
	if err != nil || got != writable {
		t.Fatalf("read-only directory not skipped: %s %v", got, err)
	}
}

func TestKindInstallReusesPathWithoutProfileChanges(t *testing.T) {
	t.Setenv("KIND_DOWNLOAD_BASE", "")
	if _, _, err := kindAsset(runtime.GOOS, runtime.GOARCH); err != nil {
		t.Skip(err)
	}
	fakeTools(t)
	root := t.TempDir()
	bin := filepath.Join(root, "custom-path-bin")
	if err := os.Mkdir(bin, 0755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", root)
	} else {
		t.Setenv("HOME", root)
	}
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("PATH", bin)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if strings.HasSuffix(r.URL.Path, ".sha256sum") {
			fmt.Fprintf(w, "%x  kind\n", sha256.Sum256(data))
			return
		}
		w.Write(data)
	}))
	defer server.Close()
	cfg := filepath.Join(root, "config.yaml")
	if err = os.WriteFile(cfg, []byte("schema_version: 1\nversions:\n  kind: v0.17.0\ninstall:\n  kind:\n    download_base: "+server.URL+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"kind", "install", "--config", cfg, "--bin-dir", filepath.Join(root, "versioned-kind"), "--download-dir", filepath.Join(root, "downloads")}
	var out bytes.Buffer
	for i := 0; i < 2; i++ {
		out.Reset()
		code, err := Run(context.Background(), args, &out, &out, "dev")
		if err != nil || code != 0 {
			t.Fatalf("install %d: %d %v %s", i, code, err, out.String())
		}
		if !strings.Contains(out.String(), "PATH 验证通过") || !strings.Contains(out.String(), bin) {
			t.Fatal(out.String())
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("same version downloaded again: %d requests", requests.Load())
	}
	for _, name := range []string{".bashrc", ".profile", ".zshrc"} {
		if _, err = os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("unnecessary profile write: %s %v", name, err)
		}
	}
}
