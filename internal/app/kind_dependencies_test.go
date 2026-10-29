package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestKindCreateMissingDependencyInstructions(t *testing.T) {
	for _, tc := range []struct{ missing, instruction string }{
		{"kubectl", "opskit kubectl install --version v1.24.7"},
		{"docker", "Docker"},
		{"kind", "opskit kind install"},
	} {
		t.Run(tc.missing, func(t *testing.T) {
			t.Chdir(t.TempDir())
			dir := fakeTools(t)
			name := tc.missing
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			code, err, _ := workflowRun(t, "kind", "create", "--version", "v1.24.7")
			if code != 3 || err == nil || !strings.Contains(err.Error(), tc.instruction) {
				t.Fatal("missing dependency did not give install instructions", code, err)
			}
		})
	}
}

func TestKindInstallWithoutKubectlOrDockerStillSucceeds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PATH exposure; Windows prerequisite errors are covered separately")
	}
	t.Chdir(t.TempDir())
	dir := fakeTools(t)
	binDir := t.TempDir()
	raw, err := os.ReadFile(filepath.Join(dir, "kind"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "kind"), raw, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"kind", "kubectl", "docker"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	var out strings.Builder
	code, err := Run(context.Background(), []string{"kind", "install", "--version", "v0.17.0", "--bin-dir", binDir, "--link-dir", dir, "--download-dir", t.TempDir()}, &out, &out, "test")
	if code != 0 || err != nil || !strings.Contains(out.String(), "opskit kubectl install") || !strings.Contains(out.String(), "sudo opskit docker install") {
		t.Fatal("kind installation blocked or dependency hints missing", code, err, out.String())
	}
}

func TestKindCreateDockerUnavailable(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := fakeTools(t)
	t.Setenv("OPSKIT_FAKE_DOCKER_UNAVAILABLE", "1")
	code, err, _ := workflowRun(t, "kind", "create")
	if code != 3 || err == nil || !strings.Contains(err.Error(), "docker info") || !strings.Contains(err.Error(), "Cannot connect to the Docker daemon") {
		t.Fatal("Docker failure lost recovery instructions or cause", code, err)
	}
	events, err := os.ReadFile(filepath.Join(dir, "events"))
	if err != nil || strings.Contains(string(events), "create cluster") {
		t.Fatal("cluster creation was attempted with unavailable Docker", err, string(events))
	}
}

func TestKindCreateDryRunWithoutDependencies(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", t.TempDir())
	code, err, output := workflowRun(t, "kind", "create", "--dry-run", "--version", "v1.25.3")
	if code != 0 || err != nil || !strings.Contains(output, "kindest/node:v1.25.3") {
		t.Fatal("preview required installed dependencies", code, err, output)
	}
}

func TestKindCreateMissingCustomBinary(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := fakeTools(t)
	binary := filepath.Join(dir, "missing-kind")
	code, err, _ := workflowRun(t, "kind", "create", "--kind-bin", binary)
	if code != 3 || err == nil || !strings.Contains(err.Error(), binary) || !strings.Contains(err.Error(), "--kind-bin") {
		t.Fatal("custom kind path failure did not identify configured binary", code, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "events")); !os.IsNotExist(err) {
		t.Fatal("missing binary check executed external commands", err)
	}
}

func TestKindInstallDependencyHints(t *testing.T) {
	dir := fakeTools(t)
	t.Setenv("PATH", dir)
	var out strings.Builder
	printKindDependencyHints(&out, runtime.GOOS, "v1.25.3")
	if out.Len() != 0 {
		t.Fatal("installed dependencies produced warnings", out.String())
	}
	t.Setenv("PATH", t.TempDir())
	for _, platform := range []string{"linux", "windows", "darwin"} {
		out.Reset()
		printKindDependencyHints(&out, platform, "v1.25.3")
		if !strings.Contains(out.String(), "opskit kubectl install --version v1.25.3") {
			t.Fatal("hint lost configured Kubernetes version", out.String())
		}
		if platform == "linux" {
			if !strings.Contains(out.String(), "sudo opskit docker install") {
				t.Fatal(out.String())
			}
		} else if !strings.Contains(out.String(), "Docker Desktop") {
			t.Fatal(out.String())
		}
	}
}
