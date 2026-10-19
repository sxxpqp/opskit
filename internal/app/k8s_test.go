package app

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func clusterTestConfig(t *testing.T, workers bool) fileConfig {
	t.Helper()
	var c fileConfig
	if err := yamlClusterDefaults(&c); err != nil {
		t.Fatal(err)
	}
	c.K8s.ControlPlaneEndpoint.Address = "192.168.10.10"
	c.K8s.Hosts = []clusterHost{{Name: "master1", Address: "192.168.10.10", InternalAddress: "192.168.10.10", User: "root", Password: "ssh-secret"}}
	if workers {
		c.K8s.Hosts = append(c.K8s.Hosts, clusterHost{Name: "worker1", Address: "192.168.10.11", InternalAddress: "192.168.10.11", User: "ubuntu", Password: "ssh-secret", SudoPassword: "sudo-secret"})
		c.K8s.RoleGroups.Worker = []string{"worker1"}
	}
	c.K8s.Network.Manifest = filepath.Join(t.TempDir(), "network.yaml")
	if err := os.WriteFile(c.K8s.Network.Manifest, []byte("apiVersion: v1\nkind: List\nitems: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c.K8s.Deploy.NodeBinaries = map[string]string{"amd64": filepath.Join(t.TempDir(), "opskit")}
	if err := os.WriteFile(c.K8s.Deploy.NodeBinaries["amd64"], []byte("mock Linux executable"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := validateCluster(&c.K8s); err != nil {
		t.Fatal(err)
	}
	c.projectComponents()
	return c
}
func TestClusterConfigVersionIsolationAndRemovedFields(t *testing.T) {
	c := clusterTestConfig(t, false)
	c.Kind.Kubernetes.Version = "v1.25.3"
	c.K8s.Kubernetes.Version = "v1.24.7"
	c.projectComponents()
	if c.Versions.Kubernetes != "v1.24.7" || c.Kind.Kubernetes.Version != "v1.25.3" {
		t.Fatal("versions leaked")
	}
	for _, body := range []string{
		"schema_version: 1\nk8s:\n  registry:\n    registryMirrors: [https://old-proxy.example]\n",
		"schema_version: 1\nk8s:\n  deploy:\n    knownHostsFile: known_hosts\n",
		"schema_version: 1\nversions:\n  kubernetes: v1.24.7\n",
		"schema_version: 1\nk8s:\n  kubectl:\n    version: v1.24.7\n",
		"schema_version: 1\nkubeadm:\n  advertise_address: 192.168.10.10\n",
		"schema_version: 1\nkubesphere:\n  password: Test1234\n",
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readFileConfig(path, true); err == nil {
			t.Fatal("removed configuration accepted", body)
		}
	}
}
func TestClusterValidation(t *testing.T) {
	for _, mutate := range []func(*clusterSettings){
		func(c *clusterSettings) { c.RoleGroups.Master = append(c.RoleGroups.Master, "worker1") },
		func(c *clusterSettings) { c.RoleGroups.Etcd = []string{"worker1"} },
		func(c *clusterSettings) { c.RoleGroups.Worker = []string{"missing"} },
		func(c *clusterSettings) { c.Hosts[1].InternalAddress = c.Hosts[0].InternalAddress },
		func(c *clusterSettings) { c.Hosts[0].PrivateKeyPath = "/tmp/key" },
		func(c *clusterSettings) { c.Containerd.Version = "v2.1.3" },
		func(c *clusterSettings) { c.Network.PodSubnet = c.Network.ServiceSubnet },
		func(c *clusterSettings) { c.Network.Manifest = ""; c.Network.Calico.Version = "v3.99.0" },
	} {
		c := clusterTestConfig(t, true)
		mutate(&c.K8s)
		if err := validateCluster(&c.K8s); err == nil {
			t.Fatal("invalid cluster accepted")
		}
	}
	c := clusterTestConfig(t, false)
	c.K8s.Kubernetes.ContainerManager = "docker"
	if err := validateCluster(&c.K8s); err != nil {
		t.Fatal(err)
	}
}
func TestClusterDryRunDoesNotReadSecrets(t *testing.T) {
	c := clusterTestConfig(t, false)
	c.K8s.Hosts[0].Password = ""
	c.K8s.Hosts[0].PasswordEnv = "OPSKIT_TEST_MISSING_SECRET"
	t.Setenv("OPSKIT_TEST_MISSING_SECRET", "")
	raw, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	code, err, text := workflowRun(t, "k8s", "create", "--config", path, "--dry-run")
	if code != 0 || err != nil || !strings.Contains(text, "KubeSphere=false") || strings.Contains(text, "ssh-secret") {
		t.Fatal(code, err, text)
	}
}

type clusterMockRemote struct {
	mu            sync.Mutex
	name          string
	events        *[]string
	eventMu       *sync.Mutex
	preflightFail bool
	uploads       int
	sawSafeConfig bool
	nodeMirrors   mirrorSettings
	planCalico    bool
	network       []byte
}

func (r *clusterMockRemote) record(s string) {
	r.eventMu.Lock()
	*r.events = append(*r.events, r.name+":"+s)
	r.eventMu.Unlock()
}
func (r *clusterMockRemote) Close() error { return nil }
func (r *clusterMockRemote) Exec(ctx context.Context, root bool, command string) ([]byte, error) {
	if strings.Contains(command, "uname -s") {
		r.record("preflight")
		if r.preflightFail {
			return nil, errors.New("sudo denied")
		}
		return []byte("Linux\nx86_64\n"), nil
	}
	if command == clusterDependenciesScript {
		r.record("dependencies")
		return []byte("System dependencies ready\n"), nil
	}
	if strings.Contains(command, "--protocol") {
		return []byte(clusterNodeProtocol + "\n"), nil
	}
	for _, phase := range []string{"probe", "setup", "init", "kubeconfig", "credentials", "join", "ready", "sphere"} {
		if strings.Contains(command, "--phase '"+phase+"'") {
			r.record(phase)
			switch phase {
			case "kubeconfig":
				return []byte("apiVersion: v1\nclusters:\n- name: kubernetes\n  cluster:\n    server: https://127.0.0.1:6443\n    certificate-authority-data: Y2E=\ncontexts: []\nusers: []\n"), nil
			case "credentials":
				return json.Marshal(clusterJoin{Token: "abcdef.0123456789abcdef", CAHash: "sha256:" + strings.Repeat("a", 64), Endpoint: "192.168.10.10:6443"})
			}
			return []byte("phase completed\n"), nil
		}
	}
	if strings.Contains(command, "token delete") {
		r.record("revoke")
	}
	return nil, nil
}
func (r *clusterMockRemote) Upload(ctx context.Context, path string, src io.Reader) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.uploads++
	if strings.HasSuffix(path, "bundle.tar") {
		tr := tar.NewReader(src)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			raw, err := io.ReadAll(tr)
			if err != nil {
				return err
			}
			if h.Name == "plan.json" {
				var plan clusterNodePlan
				if err := json.Unmarshal(raw, &plan); err != nil {
					return err
				}
				r.planCalico = plan.Calico
			}
			if h.Name == "network.yaml" {
				r.network = raw
			}
			if h.Name == "node.yaml" {
				if bytes.Contains(raw, []byte("ssh-secret")) || bytes.Contains(raw, []byte("sudo-secret")) || bytes.Contains(raw, []byte("unrelated-s3-secret")) {
					return errors.New("SSH secrets leaked to node bundle")
				}
				var config fileConfig
				if err := yaml.Unmarshal(raw, &config); err != nil {
					return err
				}
				r.nodeMirrors = config.Mirrors
				r.sawSafeConfig = true
			}
		}
	} else {
		_, err := io.Copy(io.Discard, src)
		return err
	}
	return nil
}
func TestClusterSSHPreflightStopsBeforeWrites(t *testing.T) {
	c := clusterTestConfig(t, true)
	var events []string
	var mu sync.Mutex
	var remotes []*clusterMockRemote
	dial := func(ctx context.Context, h clusterHost) (clusterRemote, error) {
		r := &clusterMockRemote{name: h.Name, events: &events, eventMu: &mu, preflightFail: h.Name == "worker1"}
		remotes = append(remotes, r)
		return r, nil
	}
	if err := executeK8sCluster(context.Background(), c, t.TempDir(), io.Discard, dial); err == nil {
		t.Fatal("failed preflight accepted")
	}
	for _, r := range remotes {
		if strings.Contains(strings.Join(events, "\n"), ":dependencies") {
			t.Fatal("installed dependencies before all-node preflight")
		}
		if r.uploads != 0 {
			t.Fatal("wrote before all-node preflight")
		}
	}
}
func TestClusterOrchestrationDownloadsOnceAndJoins(t *testing.T) {
	for _, autoCalico := range []bool{false, true} {
		t.Run(fmt.Sprintf("autoCalico=%t", autoCalico), func(t *testing.T) {

			c := clusterTestConfig(t, true)
			if autoCalico {
				c.K8s.Network.Manifest = ""
			}
			c.S3.AccessKey = "unrelated-s3-secret"
			c.Mirrors.Docker = []string{"https://docker-proxy.example"}
			c.Mirrors.Containerd["docker.io"] = registryMirror{Server: "https://registry-1.docker.io", Endpoint: "https://containerd-proxy.example/v2/dockerhub", SkipVerify: true, OverridePath: true}
			var requestMu sync.Mutex
			requests := map[string]int{}
			body := []byte("binary fixture")
			sum := sha256.Sum256(body)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestMu.Lock()
				requests[r.URL.Path]++
				requestMu.Unlock()
				if r.URL.Path == "/projectcalico/calico/v3.26.5/manifests/calico.yaml" {
					fmt.Fprint(w, calicoTestManifest)
				} else if strings.HasSuffix(r.URL.Path, "runc.sha256sum") {
					fmt.Fprintf(w, "%x  runc.amd64\n", sum)
				} else if strings.HasSuffix(r.URL.Path, ".sha256") || strings.HasSuffix(r.URL.Path, ".sha256sum") {
					fmt.Fprintf(w, "%x\n", sum)
				} else {
					_, _ = w.Write(body)
				}
			}))
			defer server.Close()
			c.Install.Calico.DownloadBase = server.URL
			calicoSum := sha256.Sum256([]byte(calicoTestManifest))
			c.Install.Calico.SHA256 = fmt.Sprintf("%x", calicoSum)
			c.Install.DownloadDir = t.TempDir()
			for _, setting := range []*componentDownloadSettings{&c.Install.Kubeadm, &c.Install.Kubelet, &c.Install.Kubectl, &c.Install.CRITools, &c.Install.Containerd, &c.Install.Runc, &c.Install.CNIPlugins} {
				setting.DownloadBase = server.URL
			}
			var events []string
			var mu sync.Mutex
			var remotes []*clusterMockRemote
			dial := func(ctx context.Context, h clusterHost) (clusterRemote, error) {
				r := &clusterMockRemote{name: h.Name, events: &events, eventMu: &mu}
				remotes = append(remotes, r)
				return r, nil
			}
			var logs bytes.Buffer
			out := &clusterLockedWriter{w: &logs}
			output := t.TempDir()
			if err := executeK8sCluster(context.Background(), c, output, out, dial); err != nil {
				t.Fatal(err)
			}
			if autoCalico && requests["/projectcalico/calico/v3.26.5/manifests/calico.yaml"] != 1 {
				t.Fatal("Calico not fetched exactly once")
			}
			for path, count := range requests {
				if count != 1 {
					t.Fatal("artifact fetched more than once", path, count)
				}
			}
			joined := strings.Join(events, "\n")
			for _, required := range []string{"master1:dependencies", "worker1:dependencies", "master1:init", "worker1:join", "master1:ready", "master1:revoke"} {
				if !strings.Contains(joined, required) {
					t.Fatal("missing stage", required, joined)
				}
			}
			if strings.Index(joined, "worker1:join") < strings.Index(joined, "master1:init") {
				t.Fatal("worker joined before control plane")
			}
			if strings.Contains(joined, ":sphere") {
				t.Fatal("KS should be disabled")
			}
			for _, r := range remotes {
				if r.planCalico != autoCalico {
					t.Fatal("incorrect network mode in node plan")
				}
				if autoCalico && !bytes.Contains(r.network, []byte("10.244.0.0/16")) {
					t.Fatal("rendered network missing in bundle")
				}
				if !reflect.DeepEqual(r.nodeMirrors, c.Mirrors) {
					t.Fatal("runtime mirror settings lost during node distribution", r.nodeMirrors)
				}
				if !r.sawSafeConfig {
					t.Fatal("node config missing")
				}
			}
			if strings.Contains(logs.String(), "abcdef.0123456789abcdef") {
				t.Fatal("join token leaked")
			}
			raw, err := os.ReadFile(filepath.Join(output, "kubeconfig"))
			if err != nil || !bytes.Contains(raw, []byte("https://192.168.10.10:6443")) {
				t.Fatal(string(raw), err)
			}

		})
	}
}

func TestClusterOfflineCacheIntegrity(t *testing.T) {
	root := t.TempDir()
	body := []byte("checked binary")
	sum := sha256.Sum256(body)
	if err := os.WriteFile(filepath.Join(root, "asset"), body, 0600); err != nil {
		t.Fatal(err)
	}
	entries := map[string]clusterCacheEntry{"https://example.com/asset": {File: "asset", SHA256: fmt.Sprintf("%x", sum)}}
	raw, _ := json.Marshal(entries)
	if err := os.WriteFile(filepath.Join(root, "cache.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), clusterCacheKey{}, root)
	var out bytes.Buffer
	if err := downloadRetained(ctx, "https://example.com/asset", &out, "", "tool", "v1.0.0"); err != nil || !bytes.Equal(out.Bytes(), body) {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "asset"), []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := cachedClusterDownload(ctx, "https://example.com/asset", io.Discard); err == nil {
		t.Fatal("corrupt bundle accepted")
	}
	if _, err := cachedClusterDownload(ctx, "https://example.com/missing", io.Discard); err == nil {
		t.Fatal("missing offline artifact allowed")
	}
}

func TestClusterReadinessRequiresEveryConfiguredNode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	err := waitClusterNodesReady(ctx, []string{"master1", "worker1"}, func() ([]byte, error) {
		cancel()
		return []byte(`{"items":[{"metadata":{"name":"master1"},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`), nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("missing worker incorrectly considered ready", err)
	}
}

func TestClusterDiscoveryHashUsesCAPublicKey(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	path := filepath.Join(t.TempDir(), "kubeconfig")
	raw := []byte("clusters:\n- cluster:\n    certificate-authority-data: " + base64.StdEncoding.EncodeToString(ca) + "\n")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	actual, err := kubeconfigCAHash(path)
	if err != nil {
		t.Fatal(err)
	}
	spki, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(spki)
	if actual != fmt.Sprintf("sha256:%x", hash) {
		t.Fatal("discovery hash must use CA public key")
	}
}

func TestClusterRuntimeMustReportReady(t *testing.T) {
	for _, raw := range []string{`{}`, `{"status":{"conditions":[{"type":"RuntimeReady","status":false}]}}`, `{"status":{"conditions":[{"type":"NetworkReady","status":true}]}}`} {
		if err := validateClusterRuntimeInfo([]byte(raw)); err == nil {
			t.Fatal("non-ready CRI accepted", raw)
		}
	}
	if err := validateClusterRuntimeInfo([]byte(`{"status":{"conditions":[{"type":"RuntimeReady","status":true},{"type":"NetworkReady","status":false}]}}`)); err != nil {
		t.Fatal(err)
	}
}

func TestClusterResumeChecksServerVersion(t *testing.T) {
	if err := validateClusterServerVersion([]byte(`{"serverVersion":{"gitVersion":"v1.24.7"}}`), "v1.24.7"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{}`, `{"serverVersion":{"gitVersion":"v1.25.3"}}`, `invalid`} {
		if err := validateClusterServerVersion([]byte(raw), "v1.24.7"); err == nil {
			t.Fatal("mismatched server accepted")
		}
	}
}

func TestSingleNodeTaintPatchPreservesOtherTaints(t *testing.T) {
	raw := []byte(`{"metadata":{"resourceVersion":"42"},"spec":{"taints":[{"key":"node-role.kubernetes.io/control-plane","effect":"NoSchedule"},{"key":"node-role.kubernetes.io/master","effect":"NoSchedule"},{"key":"maintenance","value":"keep","effect":"NoExecute"}]}}`)
	patch, err := singleNodeTaintPatch(raw)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Metadata struct {
			Version string `json:"resourceVersion"`
		} `json:"metadata"`
		Spec struct {
			Taints []map[string]any `json:"taints"`
		} `json:"spec"`
	}
	if err = json.Unmarshal(patch, &result); err != nil {
		t.Fatal(err)
	}
	if result.Metadata.Version != "42" || len(result.Spec.Taints) != 1 || result.Spec.Taints[0]["key"] != "maintenance" {
		t.Fatal(string(patch))
	}
	if patch, err := singleNodeTaintPatch([]byte(`{"metadata":{"resourceVersion":"43"},"spec":{}}`)); err != nil || len(patch) != 0 {
		t.Fatal("unneeded taint update", err)
	}
	if _, err := singleNodeTaintPatch([]byte(`{"spec":{"taints":[{"key":"node-role.kubernetes.io/master","effect":"NoSchedule"}]}}`)); err == nil {
		t.Fatal("patch without version accepted")
	}
}

type phaseCaptureRemote struct{ command string }

func (r *phaseCaptureRemote) Exec(ctx context.Context, root bool, command string) ([]byte, error) {
	r.command = command
	return nil, nil
}
func (r *phaseCaptureRemote) Upload(context.Context, string, io.Reader) error { return nil }
func (r *phaseCaptureRemote) Close() error                                    { return nil }
func TestClusterPhasePassesRemainingDeadline(t *testing.T) {
	remote := &phaseCaptureRemote{}
	node := &connectedClusterNode{host: clusterHost{Name: "master1"}, remote: remote, bundle: "/var/lib/opskit/run"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := clusterPhase(ctx, node, "setup", io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remote.command, " --timeout '") {
		t.Fatal("remaining deadline missing")
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	remote.command = ""
	if _, err := clusterPhase(canceled, node, "setup", io.Discard); !errors.Is(err, context.Canceled) || remote.command != "" {
		t.Fatal("canceled deployment invoked node", err)
	}
}

func TestClusterMirrorDefaultsAndIsolation(t *testing.T) {
	var defaults fileConfig
	if err := yamlClusterDefaults(&defaults); err != nil {
		t.Fatal(err)
	}
	c := fileConfig{}
	if err := mergeClusterDefaults(&c); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Mirrors.Docker, defaults.Mirrors.Docker) || !reflect.DeepEqual(c.Mirrors.DockerInsecure, defaults.Mirrors.DockerInsecure) || !reflect.DeepEqual(c.Mirrors.Containerd, defaults.Mirrors.Containerd) {
		t.Fatal("runtime defaults not loaded from template")
	}
	custom := fileConfig{}
	custom.Mirrors.Docker = []string{"https://docker.example"}
	custom.Mirrors.DockerInsecure = []string{}
	custom.Mirrors.Containerd = map[string]registryMirror{"docker.io": {Server: "https://registry-1.docker.io", Endpoint: "https://containerd.example/v2/hub", SkipVerify: true, OverridePath: true}}
	if err := mergeClusterDefaults(&custom); err != nil {
		t.Fatal(err)
	}
	if custom.Mirrors.Docker[0] != "https://docker.example" || len(custom.Mirrors.DockerInsecure) != 0 || custom.Mirrors.Containerd["docker.io"].Endpoint != "https://containerd.example/v2/hub" {
		t.Fatal("explicit settings overwritten")
	}
	hosts, err := renderHosts(custom.Mirrors.Containerd)
	if err != nil {
		t.Fatal(err)
	}
	text := string(hosts["docker.io"])
	for _, want := range []string{"https://containerd.example/v2/hub", "skip_verify = true", "override_path = true"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s: %s", want, text)
		}
	}
	if strings.Contains(text, "https://docker.example") {
		t.Fatal("Docker mirror leaked into containerd hosts")
	}
}

func TestClusterLogPreservesVersionsAndAddresses(t *testing.T) {
	text := "master1 v1.24.7 172.16.0.34 token=abcdef.0123456789abcdef"
	want := "master1 v1.24.7 172.16.0.34 token=<join-token>"
	if got := redactClusterLog(text); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
