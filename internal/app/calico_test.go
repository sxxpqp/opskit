package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandaloneCalicoPreflight(t *testing.T) {
	for _, tc := range []struct {
		name, version, cidr, ds string
		denied, wantError       bool
	}{
		{name: "fresh", version: "v1.24.7", cidr: "10.244.0.0/16"},
		{name: "wrong-version", version: "v1.29.0", cidr: "10.244.0.0/16", wantError: true},
		{name: "wrong-cidr", version: "v1.24.7", cidr: "192.168.0.0/16", wantError: true},
		{name: "missing-cidr", version: "v1.24.7", wantError: true},
		{name: "existing-calico", version: "v1.24.7", cidr: "10.244.0.0/16", ds: "calico-node", wantError: true},
		{name: "other-cni", version: "v1.24.7", cidr: "10.244.0.0/16", ds: "kube-flannel-ds", wantError: true},
		{name: "read-denied", version: "v1.24.7", cidr: "10.244.0.0/16", denied: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var c fileConfig
			if err := yamlClusterDefaults(&c); err != nil {
				t.Fatal(err)
			}
			kube := func(args ...string) ([]byte, error) {
				joined := strings.Join(args, " ")
				switch joined {
				case "version -o json":
					return []byte(fmt.Sprintf(`{"serverVersion":{"gitVersion":%q}}`, tc.version)), nil
				case "-n kube-system get configmap kubeadm-config -o json":
					if tc.denied {
						return nil, errors.New("forbidden")
					}
					return json.Marshal(map[string]any{"data": map[string]string{"ClusterConfiguration": "networking:\n  podSubnet: " + tc.cidr + "\n"}})
				case "get daemonsets -A -o json":
					return []byte(fmt.Sprintf(`{"items":[{"metadata":{"name":%q}}]}`, tc.ds)), nil
				default:
					t.Fatalf("preflight attempted mutation: %s", joined)
					return nil, nil
				}
			}
			if err := preflightStandaloneCalico(c.K8s, kube); (err != nil) != tc.wantError {
				t.Fatal(err)
			}
		})
	}
}

func TestCalicoStandaloneDryRunAndLocalManifest(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "")
	code, err, _ := workflowRun(t, "calico", "install", "--dry-run")
	if code != 0 || err != nil {
		t.Fatal(code, err)
	}
	for _, args := range [][]string{{"--version", "v3.99.0"}, {"--pod-subnet", "bad"}, {"--apply"}, {"--timeout", "0s"}} {
		code, err, _ := workflowRun(t, append([]string{"calico", "install", "--dry-run"}, args...)...)
		if code != 2 || err == nil {
			t.Fatal("invalid arguments accepted", args, code, err)
		}
	}
	raw, err := renderClusterCalico([]byte(calicoTestManifest), "v3.26.5", "10.244.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateStandaloneCalicoManifest(raw, "v3.26.5", "10.245.0.0/16"); err == nil {
		t.Fatal("local manifest CIDR mismatch ignored")
	}
	if err := validateStandaloneCalicoManifest([]byte(calicoTestManifest), "v3.26.5", "10.244.0.0/16"); err == nil {
		t.Fatal("implicit local pool accepted")
	}
}

func TestCalicoStandaloneApplyAndWait(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := fakeTools(t)
	t.Setenv("OPSKIT_FAKE_CURRENT_CONTEXT", "manual-kubeadm")
	raw, err := renderClusterCalico([]byte(calicoTestManifest), "v3.26.5", "10.244.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "network.yaml")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	code, err, text := workflowRun(t, "calico", "install", "--kubeconfig", "manual.conf", "--manifest", file)
	if code != 0 || err != nil || !strings.Contains(text, "就绪") {
		t.Fatal(code, err, text)
	}
	events, _ := os.ReadFile(filepath.Join(dir, "events"))
	for _, want := range []string{"version -o json", "get configmap kubeadm-config", "get daemonsets -A", "apply -f", "rollout status daemonset/calico-node", "wait --for=condition=Available deployment/calico-kube-controllers"} {
		if !strings.Contains(string(events), want) {
			t.Fatal("missing step", want, string(events))
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(string(events)), "\n") {
		if !strings.Contains(line, "--kubeconfig manual.conf") || (!strings.Contains(line, "config current-context") && !strings.Contains(line, "--context manual-kubeadm")) {
			t.Fatal("target changed", line)
		}
	}
	after, _ := os.ReadFile(file)
	if string(after) != string(raw) {
		t.Fatal("local manifest rewritten")
	}
}

type inspectTestRemote struct {
	commands []string
	fail     bool
	closed   bool
}

func (r *inspectTestRemote) Exec(_ context.Context, _ bool, cmd string) ([]byte, error) {
	r.commands = append(r.commands, cmd)
	if r.fail {
		return nil, errors.New("probe failed")
	}
	if strings.Contains(cmd, "uname -s") {
		return []byte("Linux\nx86_64\n"), nil
	}
	return []byte("disk report"), nil
}
func (r *inspectTestRemote) Upload(context.Context, string, io.Reader) error {
	return errors.New("unexpected upload")
}
func (r *inspectTestRemote) Close() error { r.closed = true; return nil }

func TestK8sCheckContinuesAndNeverMutates(t *testing.T) {
	c := clusterTestConfig(t, true)
	remotes := []*inspectTestRemote{{fail: true}, {}}
	i := 0
	err := checkClusterHosts(context.Background(), c.K8s, io.Discard, func(context.Context, clusterHost) (clusterRemote, error) {
		r := remotes[i]
		i++
		return r, nil
	})
	if err == nil || i != 2 {
		t.Fatal("failed host hid other checks", err, i)
	}
	for _, r := range remotes {
		if !r.closed {
			t.Fatal("connection leaked")
		}
		for _, cmd := range r.commands {
			for _, mutation := range []string{"apt-get install", "mkdir", "systemctl restart", "kubeadm init", "cat >", "rm -"} {
				if strings.Contains(cmd, mutation) {
					t.Fatal("check attempted mutation", cmd)
				}
			}
		}
	}
}

func TestK8sStatusReadyAndTarget(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := fakeTools(t)
	for _, ready := range []string{"True", "False", "Unknown"} {
		t.Setenv("OPSKIT_FAKE_NODE_READY", ready)
		code, err, text := workflowRun(t, "k8s", "status", "--kubeconfig", "manual.conf", "--context", "manual")
		if (code == 0) != (ready == "True") || (err == nil) != (ready == "True") || !strings.Contains(text, "node1") || !strings.Contains(text, ready) {
			t.Fatal(ready, code, err, text)
		}
	}
	events, _ := os.ReadFile(filepath.Join(dir, "events"))
	for _, line := range strings.Split(strings.TrimSpace(string(events)), "\n") {
		if !strings.Contains(line, "--kubeconfig manual.conf --context manual") || strings.Contains(line, "apply") || strings.Contains(line, "init") {
			t.Fatal("wrong target or mutation", line)
		}
	}
}
