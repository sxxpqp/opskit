package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	templates "github.com/sxxpqp/opskit"
	"gopkg.in/yaml.v3"
)

func TestClusterVersionProfilesFrom124To137(t *testing.T) {
	for minor := 24; minor <= 37; minor++ {
		t.Run(fmt.Sprintf("v1.%d", minor), func(t *testing.T) {
			var c fileConfig
			if err := yamlClusterDefaults(&c); err != nil {
				t.Fatal(err)
			}
			c.K8s.Kubernetes.Version = fmt.Sprintf("v1.%d.1", minor)
			c.K8s.Containerd.Version = "auto"
			c.K8s.Runc.Version = "auto"
			c.K8s.CRITools.Version = "auto"
			c.K8s.CNIPlugins.Version = "auto"
			c.K8s.Network.Calico.Version = "auto"
			if err := resolveClusterComponentVersions(&c); err != nil {
				t.Fatal(err)
			}
			if c.K8s.Containerd.Version == "auto" || c.K8s.Runc.Version == "auto" ||
				c.K8s.CNIPlugins.Version == "auto" || c.K8s.CRITools.Version == "auto" {
				t.Fatalf("unresolved component version: %+v", c.K8s)
			}
			gotMinor, err := kubeadmMinor(c.K8s.CRITools.Version)
			if err != nil || gotMinor != minor {
				t.Fatalf("crictl must match Kubernetes minor: %q, %v", c.K8s.CRITools.Version, err)
			}
			if c.K8s.Kubernetes.Version != c.Versions.Kubernetes || c.K8s.CRITools.Version != c.Versions.CRITools {
				t.Fatal("projected versions are stale")
			}
			if minor < 31 && c.K8s.Containerd.Version != "v1.7.24" {
				t.Fatalf("unexpected old containerd: %s", c.K8s.Containerd.Version)
			}
			if minor >= 31 && c.K8s.Containerd.Version != "v2.1.3" {
				t.Fatalf("unexpected newer containerd: %s", c.K8s.Containerd.Version)
			}
			if minor <= 28 {
				if c.K8s.Network.Calico.Version != "v3.26.5" {
					t.Fatal("expected pinned Calico for historical profile")
				}
			} else {
				if c.K8s.Network.Calico.Version != "auto" {
					t.Fatal("unknown Calico profile silently selected")
				}
				if err := validateClusterCalico(&c.K8s, minor); err == nil {
					t.Fatal("unverified CNI accepted")
				}
			}
		})
	}
	for _, version := range []string{"v1.23.17", "v1.38.0", "v2.1.0", "latest", "v1.35"} {
		if _, err := kubeadmMinor(version); err == nil {
			t.Fatalf("unsupported version %q accepted", version)
		}
	}
}

func TestClusterProfilePreservesExplicitComponents(t *testing.T) {
	var c fileConfig
	if err := yamlClusterDefaults(&c); err != nil {
		t.Fatal(err)
	}
	c.K8s.Kubernetes.Version = "v1.28.15"
	c.K8s.Containerd.Version = "v1.7.24"
	c.K8s.Runc.Version = "v1.2.3"
	c.K8s.CRITools.Version = "v1.28.0"
	c.K8s.CNIPlugins.Version = "v1.5.1"
	if err := resolveClusterComponentVersions(&c); err != nil {
		t.Fatal(err)
	}
	if c.K8s.Runc.Version != "v1.2.3" {
		t.Fatal("explicit version overridden")
	}
	c.K8s.Kubernetes.Version = "v1.35.1"
	if err := resolveClusterComponentVersions(&c); err != nil {
		t.Fatal(err)
	}
	if err := validateCluster(&c.K8s); err == nil {
		t.Fatal("incompatible explicit crictl version should fail")
	}
}

func TestKubeadmConfigVersionAndNodeArgs(t *testing.T) {
	for _, minor := range []int{24, 28, 30, 31, 34, 37} {
		t.Run(fmt.Sprint(minor), func(t *testing.T) {
			o := kubeadmSettings{
				CRISocket: "unix:///run/containerd/containerd.sock",
				NodeName: "master",
				NodeIP: "192.168.10.10",
				AdvertiseAddress: "192.168.10.10",
				PodSubnet: "10.244.0.0/16",
				ServiceSubnet: "10.96.0.0/12",
				ImageRepository: "registry.k8s.io",
			}
			version := fmt.Sprintf("v1.%d.0", minor)
			raw, err := renderKubeadm("init", version, o)
			if err != nil {
				t.Fatal(err)
			}
			decoder := yaml.NewDecoder(bytes.NewReader(raw))
			var init map[string]any
			if err := decoder.Decode(&init); err != nil {
				t.Fatal(err)
			}
			if init["apiVersion"] != kubeadmConfigAPIVersion(minor) {
				t.Fatalf("wrong init API version: %s", raw)
			}
			reg, ok := init["nodeRegistration"].(map[string]any)
			if !ok {
				t.Fatal("nodeRegistration missing")
			}
			args := reg["kubeletExtraArgs"]
			if minor >= 31 {
				pairs, ok := args.([]any)
				if !ok || len(pairs) != 1 {
					t.Fatalf("v1beta4 requires Arg list: %#v", args)
				}
				arg, ok := pairs[0].(map[string]any)
				if !ok || arg["name"] != "node-ip" || arg["value"] != o.NodeIP {
					t.Fatalf("invalid v1beta4 kubelet args: %#v", args)
				}
			} else {
				kv, ok := args.(map[string]any)
				if !ok || kv["node-ip"] != o.NodeIP {
					t.Fatalf("v1beta3 requires key/value args: %#v", args)
				}
			}
			var cluster map[string]any
			if err := decoder.Decode(&cluster); err != nil || cluster["apiVersion"] != kubeadmConfigAPIVersion(minor) {
				t.Fatalf("wrong cluster API version: %s, err=%v", raw, err)
			}

			o.JoinEndpoint = "192.168.10.10:6443"
			o.Token = "abcdef.0123456789abcdef"
			o.CAHash = "sha256:" + strings.Repeat("b", 64)
			joined, err := renderKubeadm("join", version, o)
			if err != nil {
				t.Fatal(err)
			}
			var join map[string]any
			if err := yaml.Unmarshal(joined, &join); err != nil || join["apiVersion"] != kubeadmConfigAPIVersion(minor) {
				t.Fatalf("wrong join schema: %s, %v", joined, err)
			}
		})
	}
}

func TestKubeadmInstallAutoCRIToolsForNewerVersion(t *testing.T) {
	config := kubeadmTestConfig(t)
	code, err, output := workflowRun(t, "kubeadm", "install", "--config", config, "--version", "v1.35.1", "--dry-run")
	if code != 0 || err != nil {
		t.Fatal(code, err, output)
	}
	for _, want := range []string{"v1.35.1", "crictl v1.35.0"} {
		if !strings.Contains(output, want) {
			t.Fatalf("missing auto-selected %s in: %s", want, output)
		}
	}
	if strings.Contains(output, "v1.24.2") {
		t.Fatal("old crictl version leaked into newer Kubernetes install")
	}
}

func TestKubeadmSchemaDefaultAndExtraYAML(t *testing.T) {
	var c fileConfig
	if err := yamlClusterDefaults(&c); err != nil {
		t.Fatal(err)
	}
	if c.K8s.Kubernetes.Version != "v1.24.7" || c.K8s.CRITools.Version != "v1.24.2" {
		t.Fatalf("defaults not resolved: %s / %s", c.K8s.Kubernetes.Version, c.K8s.CRITools.Version)
	}
}

func TestK8sCreateNewerVersionRequiresPinnedNetwork(t *testing.T) {
	var c fileConfig
	if err := yaml.Unmarshal(templates.ConfigTemplate, &c); err != nil {
		t.Fatal(err)
	}
	// Use a new, valid Kubernetes patch version; all dependency versions in
	// the YAML are set to auto, and so follow this target.
	c.K8s.Kubernetes.Version = "v1.35.1"
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	write := func() {
		t.Helper()
		raw, err := yaml.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(configPath, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	code, err, output := workflowRun(t, "k8s", "create", "--config", configPath, "--dry-run")
	if code == 0 || err == nil || !strings.Contains(err.Error()+" "+output, "network.manifest") {
		t.Fatalf("newer Kubernetes without CNI manifest was accepted: %d %v %s", code, err, output)
	}
	manifest := filepath.Join(t.TempDir(), "cni.yaml")
	if err := os.WriteFile(manifest, []byte("apiVersion: v1\nkind: List\nitems: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c.K8s.Network.Manifest = manifest
	write()
	code, err, output = workflowRun(t, "k8s", "create", "--config", configPath, "--dry-run")
	if code != 0 || err != nil || !strings.Contains(output, "Kubernetes v1.35.1") || !strings.Contains(output, "CNI 本地清单") || !strings.Contains(output, "crictl=v1.35.0") || !strings.Contains(output, "containerd=v2.1.3") {
		t.Fatalf("valid newer Kubernetes with explicit network rejected: %d %v %s", code, err, output)
	}
}

