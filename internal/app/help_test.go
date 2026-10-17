package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGroupHelpWithoutConfigOrTools(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.Mkdir("config", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("config", "config.yaml"), []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	for _, group := range []string{"kind", "mirror", "docker", "cri-dockerd", "containerd", "kubesphere", "config", "etcd", "s3"} {
		for _, help := range []string{"", "-h", "--help", "help"} {
			t.Run(group+"/"+help, func(t *testing.T) {
				args := []string{group}
				if help != "" {
					args = append(args, help)
				}
				var out, diag bytes.Buffer
				code, err := Run(context.Background(), args, &out, &diag, "test")
				if code != 0 || err != nil {
					t.Fatalf("%d %v %s", code, err, diag.String())
				}
				if !strings.Contains(out.String(), "用法: opskit "+group+" ") {
					t.Fatal(out.String())
				}
				if diag.Len() != 0 {
					t.Fatal(diag.String())
				}
			})
		}
	}
	for _, help := range []string{"-h", "--help", "help"} {
		var out, diag bytes.Buffer
		if code, err := Run(context.Background(), []string{help}, &out, &diag, "test"); code != 0 || err != nil || !strings.Contains(out.String(), "kind") {
			t.Fatalf("root help %q: %d %v %s", help, code, err, out.String())
		}
	}
	for _, args := range [][]string{{"unknown"}, {"kind", "unknown"}, {"kind", "--unknown"}, {"kind", "--help", "unexpected"}} {
		var out, diag bytes.Buffer
		if code, err := Run(context.Background(), args, &out, &diag, "test"); code == 0 || err == nil {
			t.Fatalf("unknown command accepted: %v", args)
		}
	}
}
