package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// These tests only run when launched by tests/e2e/docker/run.sh.
// Unlike the mock SSH suite, these connect to two separate Linux containers
// where PID 1 is systemd and SSH/sudo run as real services.
func requireDockerK8sE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("OPSKIT_DOCKER_E2E") != "1" {
		t.Skip("Docker SSH/systemd integration test is opt-in")
	}
}

func TestDockerK8sSSHSystemdPreflight(t *testing.T) {
	requireDockerK8sE2E(t)
	passwordBytes, err := os.ReadFile("/run/e2e/password")
	if err != nil {
		t.Fatal(err)
	}
	password := strings.TrimSpace(string(passwordBytes))
	if password == "" {
		t.Fatal("missing ephemeral test password")
	}
	t.Setenv("OPSKIT_DOCKER_E2E_PASSWORD", password)

	for _, host := range []string{"master", "worker"} {
		for _, method := range []string{"password", "private-key"} {
			t.Run(host+"/"+method, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()

				h := clusterHost{
					Name: host, Address: host, Port: 22, User: "opskit",
					SudoPasswordEnv: "OPSKIT_DOCKER_E2E_PASSWORD",
				}
				if method == "password" {
					h.PasswordEnv = "OPSKIT_DOCKER_E2E_PASSWORD"
				} else {
					h.PrivateKeyPath = "/run/e2e/id_ed25519"
				}
				remote, err := dialClusterSSH(ctx, h)
				if err != nil {
					t.Fatalf("real SSH connection failed: %v", err)
				}
				defer remote.Close()

				raw, err := remote.Exec(ctx, true, clusterHostPreflightCommand("opskit-e2e"))
				if err != nil {
					t.Fatalf("real root/systemd/OS preflight failed: %v", err)
				}
				fields := strings.Fields(string(raw))
				if len(fields) != 2 || fields[0] != "Linux" ||
					(fields[1] != "x86_64" && fields[1] != "aarch64" && fields[1] != "amd64" && fields[1] != "arm64") {
					t.Fatalf("unexpected preflight result: %q", raw)
				}

				service, err := remote.Exec(ctx, true, "systemctl is-active ssh")
				if err != nil || strings.TrimSpace(string(service)) != "active" {
					t.Fatalf("SSH service is not running under systemd: %q, %v", service, err)
				}

				// Check the same Upload implementation used by actual k8s create.
				path := "/tmp/opskit-e2e-" + method + "-file"
				payload := "opskit-e2e-data:" + host + ":" + method
				if err := remote.Upload(ctx, path, strings.NewReader(payload)); err != nil {
					t.Fatalf("SSH upload failed: %v", err)
				}
				defer remote.Exec(context.Background(), false, "rm -f -- "+shellQuote(path))
				got, err := remote.Exec(ctx, false, "cat -- "+shellQuote(path))
				if err != nil || string(got) != payload {
					t.Fatalf("remote upload content mismatch: %q %v", got, err)
				}
				perm, err := remote.Exec(ctx, false, "stat -c %a -- "+shellQuote(path))
				if err != nil || strings.TrimSpace(string(perm)) != "600" {
					t.Fatalf("remote upload not private: %q %v", perm, err)
				}
			})
		}
	}

	// Wrong SSH authentication must never be interpreted as a successful preflight.
	bad := clusterHost{
		Name: "master", Address: "master", Port: 22, User: "opskit",
		Password: "incorrect-e2e-credential",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if remote, err := dialClusterSSH(ctx, bad); err == nil {
		remote.Close()
		t.Fatal("invalid SSH password unexpectedly accepted")
	}
}

func TestDockerK8sCreateDryRun(t *testing.T) {
	requireDockerK8sE2E(t)

	var config fileConfig
	if err := yamlClusterDefaults(&config); err != nil {
		t.Fatal(err)
	}
	config.K8s.Name = "opskit-e2e"
	config.K8s.Hosts = []clusterHost{
		{Name: "master", Address: "master", InternalAddress: "10.89.0.10", Port: 22, User: "opskit", PasswordEnv: "OPSKIT_DOCKER_E2E_PASSWORD", SudoPasswordEnv: "OPSKIT_DOCKER_E2E_PASSWORD"},
		{Name: "worker", Address: "worker", InternalAddress: "10.89.0.11", Port: 22, User: "opskit", PrivateKeyPath: "/run/e2e/id_ed25519", SudoPasswordEnv: "OPSKIT_DOCKER_E2E_PASSWORD"},
	}
	config.K8s.RoleGroups.Etcd = []string{"master"}
	config.K8s.RoleGroups.Master = []string{"master"}
	config.K8s.RoleGroups.Worker = []string{"worker"}
	config.K8s.KubeSphere.Enabled = false

	raw, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "lab.yaml")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	code, runErr := Run(ctx, []string{"k8s", "create", "--config", path, "--dry-run"}, &out, &diag, "docker-e2e")
	if code != 0 || runErr != nil {
		t.Fatalf("real CLI dry run failed: code=%d err=%v stdout=%s stderr=%s", code, runErr, out.String(), diag.String())
	}
	for _, want := range []string{"master", "worker", "预览完成"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run missing %q: %s", want, out.String())
		}
	}
	if strings.Contains(out.String()+diag.String(), "incorrect-e2e-credential") {
		t.Fatal("dry-run leaked test credentials")
	}
	t.Log(fmt.Sprintf("real CLI dry-run validated %d Docker SSH nodes without changing hosts", len(config.K8s.Hosts)))
}
