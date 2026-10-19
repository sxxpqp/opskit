package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCobraCommandTree(t *testing.T) {
	root, err := newRootCommand(context.Background(), io.Discard, io.Discard, "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"config init", "go install", "k8s create", "k8s node",
		"kubeadm install", "kubeadm prepare", "kubeadm init", "kubeadm join",
		"kubectl install", "docker install", "cri-dockerd install", "containerd install",
		"kind install", "kind create", "kind status", "kind logs", "kind delete", "kind use", "kind unuse",
		"mirror docker", "mirror containerd", "mirror go", "mirror npm", "mirror pip", "mirror maven",
		"kubesphere install", "etcd backup", "etcd check", "s3 upload", "s3 check",
	} {
		cmd, remaining, err := root.Find(strings.Fields(path))
		if err != nil || len(remaining) != 0 || cmd.RunE == nil {
			t.Fatalf("unregistered command %s: %v %v", path, remaining, err)
		}
		if cmd.Flags().Lookup("config") == nil && path != "k8s node" {
			t.Errorf("missing config flag: %s", path)
		}
		if cmd.Flags().Lookup("apply") != nil {
			t.Errorf("removed apply flag registered: %s", path)
		}
	}
	node, _, _ := root.Find([]string{"k8s", "node"})
	if !node.Hidden {
		t.Fatal("internal node command exposed")
	}
}
func TestCobraLeafHelpWithoutConfigOrTools(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("PATH", "")
	config := filepath.Join(root, "broken.yaml")
	if err := os.WriteFile(config, []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"config init", "go install", "k8s create", "kubeadm install", "kubeadm init", "kubectl install", "kind install", "kind create", "mirror go", "kubesphere install", "docker install", "containerd install", "cri-dockerd install", "etcd backup", "s3 upload"} {
		t.Run(path, func(t *testing.T) {
			var out, diag bytes.Buffer
			args := append(strings.Fields(path), "--config", config, "--help")
			code, err := Run(context.Background(), args, &out, &diag, "test")
			if code != 0 || err != nil || !strings.Contains(out.String(), "--config") || !strings.Contains(out.String(), "用法: opskit "+path) || diag.Len() != 0 {
				t.Fatalf("%d %v\n%s\n%s", code, err, &out, &diag)
			}
		})
	}
}
func TestCobraInterspersedFlagsAndExitCodes(t *testing.T) {
	t.Chdir(t.TempDir())
	// Parsing flags after FILE must reach file inspection, without making an S3 request.
	missing := "does-not-exist.snapshot"
	code, err := Run(context.Background(), []string{"s3", "upload", missing, "--bucket", "test", "--key", "snapshot"}, io.Discard, io.Discard, "test")
	if code != 1 || err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("interspersed flags: %d %v", code, err)
	}
	for _, args := range [][]string{{"unknown"}, {"kind", "unknown"}, {"version", "extra"}, {"go", "install", "--apply"}, {"kubectl", "install", "--download-base", "https://old.example"}} {
		if code, err := Run(context.Background(), args, io.Discard, io.Discard, "test"); code != 2 || err == nil {
			t.Fatalf("%v: %d %v", args, code, err)
		}
	}
	// Each execution builds a fresh command tree: parsing cannot leak between calls.
	var out bytes.Buffer
	if code, err := Run(context.Background(), []string{"version"}, &out, io.Discard, "test-version"); code != 0 || err != nil || strings.TrimSpace(out.String()) != "test-version" {
		t.Fatalf("%d %v %s", code, err, &out)
	}
}
func TestCobraCompletionDoesNotLoadConfig(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("PATH", "")
	if err := os.Mkdir("config", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("config", "config.yaml"), []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		var out, diag bytes.Buffer
		code, err := Run(context.Background(), []string{"completion", shell}, &out, &diag, "test")
		if code != 0 || err != nil || out.Len() < 100 || diag.Len() != 0 {
			t.Fatalf("%s: %d %v %s", shell, code, err, &diag)
		}
	}
}
