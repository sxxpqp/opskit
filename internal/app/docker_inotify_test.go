package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func inotifyFixture(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sysctl fixture")
	}
	root := t.TempDir()
	script := `#!/bin/sh
case "$1" in
  -n)
    IFS= read -r value < "$OPSKIT_TEST_INOTIFY_STATE/$2" || exit 4
    printf '%s\n' "$value"
    ;;
  -p)
    [ "$OPSKIT_TEST_INOTIFY_FAIL" != 1 ] || exit 5
    [ "$OPSKIT_TEST_INOTIFY_NO_APPLY" != 1 ] || exit 0
    while read -r key equal value rest; do
      case "$key" in
        fs.inotify.max_user_instances|fs.inotify.max_user_watches)
          printf '%s\n' "$value" > "$OPSKIT_TEST_INOTIFY_STATE/$key" || exit 6
          ;;
      esac
    done < "$2"
    ;;
  *) exit 7 ;;
esac
`
	if err := os.WriteFile(filepath.Join(root, "sysctl"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"fs.inotify.max_user_instances", "fs.inotify.max_user_watches"} {
		if err := os.WriteFile(filepath.Join(root, key), []byte("128\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OPSKIT_TEST_INOTIFY_STATE", root)
	t.Setenv("OPSKIT_TEST_INOTIFY_FAIL", "")
	t.Setenv("OPSKIT_TEST_INOTIFY_NO_APPLY", "")
	return root
}
func TestDockerInotifyApplyAndPreserveHigherValues(t *testing.T) {
	for _, higher := range []bool{false, true} {
		t.Run(map[bool]string{false: "minimum", true: "preserve-higher"}[higher], func(t *testing.T) {
			root := inotifyFixture(t)
			file := filepath.Join(root, "99-inotify.conf")
			old := "# preserve this comment\nfs.inotify.max_user_instances = 64\nfs.inotify.max_user_watches = 128\n"
			if higher {
				if err := os.WriteFile(filepath.Join(root, "fs.inotify.max_user_instances"), []byte("16384\n"), 0600); err != nil {
					t.Fatal(err)
				}
				old = strings.Replace(old, "max_user_watches = 128", "max_user_watches = 1048576", 1)
			}
			if err := os.WriteFile(file, []byte(old), 0600); err != nil {
				t.Fatal(err)
			}
			if err := configureDockerInotify(context.Background(), file, io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			instances, watches := "8192", "524288"
			if higher {
				instances, watches = "16384", "1048576"
			}
			for _, want := range []string{"# preserve this comment", "max_user_instances = " + instances, "max_user_watches = " + watches} {
				if !strings.Contains(string(got), want) {
					t.Fatalf("missing %s: %s", want, got)
				}
			}
			backups, _ := filepath.Glob(file + ".backup-*")
			if len(backups) != 1 {
				t.Fatal("original file not backed up")
			}
			original, _ := os.ReadFile(backups[0])
			if string(original) != old {
				t.Fatal("backup changed")
			}
			if err := configureDockerInotify(context.Background(), file, io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			backups, _ = filepath.Glob(file + ".backup-*")
			if len(backups) != 1 {
				t.Fatal("idempotent invocation created more backups")
			}
		})
	}
}
func TestDockerInotifyFailuresAreReported(t *testing.T) {
	for _, mode := range []string{"apply-failed", "readback-failed", "invalid-current", "invalid-persistent"} {
		t.Run(mode, func(t *testing.T) {
			root := inotifyFixture(t)
			file := filepath.Join(root, "99-inotify.conf")
			switch mode {
			case "apply-failed":
				t.Setenv("OPSKIT_TEST_INOTIFY_FAIL", "1")
			case "readback-failed":
				t.Setenv("OPSKIT_TEST_INOTIFY_NO_APPLY", "1")
			case "invalid-current":
				if err := os.WriteFile(filepath.Join(root, "fs.inotify.max_user_instances"), []byte("bad\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "invalid-persistent":
				if err := os.WriteFile(file, []byte("fs.inotify.max_user_instances = bad\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := configureDockerInotify(context.Background(), file, io.Discard, io.Discard); err == nil {
				t.Fatal("failure ignored")
			}
			if mode == "invalid-current" {
				if _, err := os.Stat(file); !os.IsNotExist(err) {
					t.Fatal("wrote settings after failed read")
				}
			}
		})
	}
}
