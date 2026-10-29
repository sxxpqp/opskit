package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestKubeBlocksPreviewOffline(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", t.TempDir())
	for _, action := range []string{"install", "deploy", "status"} {
		code, err, output := workflowRun(t, "kubeblocks", action, "--dry-run")
		if code != 0 || err != nil || !strings.Contains(output, "allow_colocation=false") {
			t.Fatal(code, err, output)
		}
	}
}
func TestKubeBlocksProtectionAndOwnership(t *testing.T) {
	for _, engine := range []string{"mysql", "redis", "minio"} {
		c, err := kubeBlocksDefaults(kubeBlocksSettings{Engine: engine})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := renderKubeBlocks(c)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "password:") || !strings.Contains(string(raw), "DoNotTerminate") || !strings.Contains(string(raw), "requiredDuringSchedulingIgnoredDuringExecution") {
			t.Fatal(string(raw))
		}
		var obj map[string]any
		if err = yaml.Unmarshal(raw, &obj); err != nil {
			t.Fatal(err)
		}
		actual, _ := json.Marshal(obj)
		if err = kubeBlocksOwned(actual, raw); err != nil {
			t.Fatal(err)
		}
		obj["spec"].(map[string]any)["terminationPolicy"] = "Delete"
		actual, _ = json.Marshal(obj)
		if err = kubeBlocksOwned(actual, raw); err == nil {
			t.Fatal("accepted external destructive policy change")
		}
		delete(obj["metadata"].(map[string]any), "labels")
		actual, _ = json.Marshal(obj)
		if err = kubeBlocksOwned(actual, raw); err == nil {
			t.Fatal("adopted unmanaged cluster")
		}
	}
}
func TestKubeBlocksRejectsInsufficientNodes(t *testing.T) {
	c, err := kubeBlocksDefaults(kubeBlocksSettings{})
	if err != nil {
		t.Fatal(err)
	}
	kube := func(args ...string) ([]byte, error) {
		if args[1] == "nodes" {
			return []byte(`{"items":[{"metadata":{"labels":{"kubernetes.io/os":"linux"}},"spec":{},"status":{"conditions":[{"type":"Ready","status":"True"}]}},{"metadata":{"labels":{"kubernetes.io/os":"linux"}},"spec":{"taints":[{"effect":"NoSchedule"}]},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`), nil
		}
		return []byte("present"), nil
	}
	if err = preflightKubeBlocks(kube, c); err == nil {
		t.Fatal("accepted one usable node as HA")
	}
	c.AllowColocation = true
	if err = preflightKubeBlocks(kube, c); err != nil {
		t.Fatal(err)
	}
	kube = func(...string) ([]byte, error) { return nil, errors.New("forbidden") }
	if err = preflightKubeBlocks(kube, c); err == nil {
		t.Fatal("ignored preflight error")
	}
}
func TestKubeBlocksCRDValidation(t *testing.T) {
	f := filepath.Join(t.TempDir(), "crds.yaml")
	for _, raw := range []string{"apiVersion: v1\nkind: Secret\nmetadata:\n  name: unexpected\n", "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: other.example.org\nspec:\n  group: example.org\n"} {
		if err := os.WriteFile(f, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if err := validateKubeBlocksCRDs(f); err == nil {
			t.Fatal("accepted unrelated or incomplete manifest")
		}
	}
}
