package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestClusterProbeStandaloneKubectl(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	c := clusterTestConfig(t, false)
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write("kubectl", `printf '%s\n' '{"clientVersion":{"gitVersion":"`+c.K8s.Kubernetes.Version+`"}}'`)
	if err := probeClusterInstalled(context.Background(), c); err != nil {
		t.Fatalf("standalone matching kubectl rejected: %v", err)
	}
	write("kubectl", `printf '%s\n' '{"clientVersion":{"gitVersion":"v1.23.0"}}'`)
	if err := probeClusterInstalled(context.Background(), c); err == nil {
		t.Fatal("conflicting kubectl accepted")
	}
	write("kubectl", `printf '%s\n' '{"clientVersion":{"gitVersion":"`+c.K8s.Kubernetes.Version+`"}}'`)
	write("kubeadm", "echo "+c.K8s.Kubernetes.Version)
	if err := probeClusterInstalled(context.Background(), c); err == nil {
		t.Fatal("partial server tools accepted")
	}
}
