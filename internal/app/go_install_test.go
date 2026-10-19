package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func goTestArchive(t *testing.T, headers []*tar.Header, bodies []string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tr := tar.NewWriter(gz)
	for i, h := range headers {
		if err := tr.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.Write([]byte(bodies[i])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestGoArchiveRejectsUnsafeEntries(t *testing.T) {
	for _, h := range []*tar.Header{
		{Name: "go/../../escape", Typeflag: tar.TypeReg},
		{Name: "/go/escape", Typeflag: tar.TypeReg},
		{Name: "go/bin/link", Typeflag: tar.TypeSymlink, Linkname: "../../escape"},
		{Name: "go/bin/link", Typeflag: tar.TypeLink, Linkname: "go/bin/go"},
		{Name: "other/bin/go", Typeflag: tar.TypeReg},
	} {
		t.Run(h.Name+fmt.Sprint(h.Typeflag), func(t *testing.T) {
			b := goTestArchive(t, []*tar.Header{h}, []string{""})
			if err := extractGoSDK(context.Background(), bytes.NewReader(b), t.TempDir()); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
func TestGoInstallDryRunAndPrecedence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix SDK installer")
	}
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", root)
	config := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(config, []byte("schema_version: 1\ngo:\n  version: 1.26.2\ninstall:\n  go:\n    download_base: https://yaml.example/go\n    checksum_url: https://yaml.example/releases.json\nmirrors:\n  go: https://proxy.example,direct\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_DOWNLOAD_BASE", "https://env.example/go")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "https://env.example/go/go1.26.2."},
		{[]string{"--go-download-base", "https://cli.example/go", "--version", "go1.27.2"}, "https://cli.example/go/go1.27.2."},
	} {
		var out, diag bytes.Buffer
		args := append([]string{"go", "install", "--config", config, "--dry-run"}, tc.args...)
		code, err := Run(context.Background(), args, &out, &diag, "test")
		if code != 0 || err != nil || !strings.Contains(out.String(), tc.want) || !strings.Contains(out.String(), "GOPROXY: https://proxy.example,direct") {
			t.Fatalf("%d %v %s %s", code, err, out.String(), diag.String())
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".local")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote SDK: %v", err)
	}
	for _, flags := range [][]string{{"--version", "latest"}, {"--apply"}, {"--download-base", "https://old.example"}, {"--proxy", "bad"}, {"--timeout", "0s"}} {
		args := append([]string{"go", "install", "--config", config, "--dry-run"}, flags...)
		if code, err := Run(context.Background(), args, io.Discard, io.Discard, "test"); code == 0 || err == nil {
			t.Fatalf("invalid flags accepted: %v", flags)
		}
	}
}
func TestGoInstallSDKAndProxy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX script fixtures")
	}
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", root)
	links := filepath.Join(root, "bin")
	if err := os.Mkdir(links, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", links+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOROOT", "/wrong/sdk")
	t.Setenv("GOTOOLCHAIN", "go0.0.0")
	envfile := filepath.Join(root, "goenv")
	t.Setenv("GOENV", envfile)
	t.Setenv("GOPROXY", "")
	if err := os.WriteFile(envfile, []byte("GOFLAGS=-mod=readonly\nGOPROXY=off\n"), 0600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\n[ -z \"$GOROOT\" ] || exit 41\n[ \"$GOTOOLCHAIN\" = local ] || exit 42\ncase \"$1\" in\nversion) echo 'go version go1.26.2 %s/%s';;\nenv) echo \"$GOENV\";;\n*) exit 43;;\nesac\n", runtime.GOOS, runtime.GOARCH)
	archive := goTestArchive(t, []*tar.Header{
		{Name: "go/bin/go", Mode: 0755, Size: int64(len(script)), Typeflag: tar.TypeReg},
		{Name: "go/bin/gofmt", Mode: 0755, Size: int64(len("#!/bin/sh\nexit 0\n")), Typeflag: tar.TypeReg},
		{Name: "go/src/runtime/runtime.go", Mode: 0644, Size: 7, Typeflag: tar.TypeReg},
	}, []string{script, "#!/bin/sh\nexit 0\n", "package"})
	sum := sha256.Sum256(archive)
	asset := fmt.Sprintf("go1.26.2.%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metadata" {
			fmt.Fprintf(w, `[{"version":"go1.26.2","files":[{"filename":%q,"sha256":%q}]}]`, asset, hex.EncodeToString(sum[:]))
			return
		}
		if r.URL.Path == "/"+asset {
			_, _ = w.Write(archive)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	install := filepath.Join(root, "sdk")
	args := []string{"go", "install", "--version", "1.26.2", "--go-download-base", server.URL, "--checksum-url", server.URL + "/metadata", "--proxy", "https://proxy.example,direct", "--install-dir", install, "--link-dir", links, "--download-dir", filepath.Join(root, "downloads")}
	var out, diag bytes.Buffer
	if code, err := Run(context.Background(), args, &out, &diag, "test"); code != 0 || err != nil {
		t.Fatalf("%d %v\n%s\n%s", code, err, &out, &diag)
	}
	b, err := os.ReadFile(envfile)
	if err != nil || !strings.Contains(string(b), "GOPROXY=https://proxy.example,direct") || !strings.Contains(string(b), "GOFLAGS=-mod=readonly") {
		t.Fatalf("proxy config %s %v", b, err)
	}
	backups, err := filepath.Glob(envfile + ".backup-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups: %v %v", backups, err)
	}
	for _, name := range []string{"go", "gofmt"} {
		target, err := filepath.EvalSymlinks(filepath.Join(links, name))
		if err != nil || !sameGoTestPath(target, filepath.Join(install, "bin", name)) {
			t.Fatalf("entry %s %s %v", name, target, err)
		}
	}
	if _, err := os.Stat(filepath.Join(install, "src", "runtime", "runtime.go")); err != nil {
		t.Fatal("full tree missing", err)
	}
	// A second invocation cannot merge or overwrite an existing SDK.
	if code, err := Run(context.Background(), args, io.Discard, io.Discard, "test"); code == 0 || err == nil {
		t.Fatal("existing SDK overwritten")
	}
}
func TestGoBadChecksumDoesNotInstall(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sdk")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metadata" {
			fmt.Fprintf(w, `[{"version":"go1.26.2","files":[{"filename":"sdk.tar.gz","sha256":%q}]}]`, strings.Repeat("0", 64))
			return
		}
		fmt.Fprint(w, "corrupt SDK")
	}))
	defer server.Close()
	err := installGoSDK(context.Background(), server.URL+"/sdk.tar.gz", server.URL+"/metadata", "sdk.tar.gz", "1.26.2", target, filepath.Join(root, "downloads"))
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("bad SDK installed: %v", err)
	}
}
func TestGoMetadataRequiresExactVersionAndAsset(t *testing.T) {
	for _, raw := range []string{`[]`, `[{"version":"go1.26.1","files":[{"filename":"sdk","sha256":"00"}]}]`, `[{"version":"go1.26.2","files":[{"filename":"sdk","sha256":"bad"}]}]`} {
		if _, err := goArchiveChecksum([]byte(raw), "sdk", "1.26.2"); err == nil {
			t.Fatal("invalid metadata accepted", raw)
		}
	}
}

func sameGoTestPath(actual, expected string) bool {
	resolved, err := filepath.EvalSymlinks(expected)
	return err == nil && actual == resolved
}

func TestGoProxyDisabledDoesNotWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "go")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho off\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := configureGoProxy(context.Background(), binary, "https://proxy.example,direct", io.Discard); err == nil || !strings.Contains(err.Error(), "GOENV") {
		t.Fatalf("%v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("unexpected files %v %v", entries, err)
	}
}
