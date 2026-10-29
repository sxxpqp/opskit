package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func middlewareTestSettings(t *testing.T, engine string) middlewareSettings {
	t.Helper()
	c, err := middlewareDefaults(middlewareSettings{Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMiddlewarePrivateEnvironmentWins(t *testing.T) {
	fakeTools(t)
	t.Setenv("OPSKIT_CLUSTER_PASSWORD", "UnrelatedGlobalValue")
	c := middlewareTestSettings(t, "redis")
	var out bytes.Buffer
	if err := middlewareRun(context.Background(), c, t.TempDir(), &out, &out, "opskit-middleware-env-probe"); err != nil {
		t.Fatal(err)
	}
	if out.String() != "internal=" {
		t.Fatal("generic host environment overrode the selected component credentials", out.String())
	}
}

func TestMiddlewareDryRunWithoutDependencies(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("OPSKIT_REDIS_CLUSTER_PASSWORD", "")
	for _, engine := range []string{"mysql", "redis", "minio"} {
		dir := filepath.Join(t.TempDir(), "absent")
		code, err, output := workflowRun(t, "middleware", "deploy", "--engine", engine, "--directory", dir, "--dry-run")
		if code != 0 || err != nil || !strings.Contains(output, `"failure_scope":"process-only`) {
			t.Fatal(code, err, output)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("preview created deployment files")
		}
	}
	if code, err, _ := workflowRun(t, "middleware", "recover", "--engine", "redis", "--dry-run"); code != 2 || err == nil {
		t.Fatal("preview accepted unsupported Redis recovery", code, err)
	}
}

func TestMiddlewareRenderTopologyAndCredentialIsolation(t *testing.T) {
	for engine, want := range map[string]int{"mysql": 3, "redis": 6, "minio": 4} {
		c := middlewareTestSettings(t, engine)
		b, err := renderMiddleware(c)
		if err != nil || len(b.Nodes) != want {
			t.Fatal(engine, err, len(b.Nodes))
		}
		if !strings.Contains(string(b.Files["compose.yaml"]), "${OPSKIT_CLUSTER_PASSWORD") {
			t.Fatal("rendered secret must be a reference")
		}
		var doc struct {
			Services map[string]struct {
				Profiles []string `yaml:"profiles"`
			} `yaml:"services"`
		}
		if err := yaml.Unmarshal(b.Files["compose.yaml"], &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.Services["check"].Profiles) != 1 {
			t.Fatal("check container would start as a service")
		}
		if engine == "mysql" && len(doc.Services["router"].Profiles) != 1 {
			t.Fatal("router starts before metadata exists")
		}
		if engine == "mysql" {
			var parsed struct {
				Services map[string]struct {
					Environment map[string]string `yaml:"environment"`
				} `yaml:"services"`
			}
			if err := yaml.Unmarshal(b.Files["compose.yaml"], &parsed); err != nil {
				t.Fatal(err)
			}
			if parsed.Services["mysql1"].Environment["MYSQL_PWD"] != "" {
				t.Fatal("MYSQL_PWD breaks the server entrypoint's initial password-free bootstrap")
			}
		}
	}
	c := middlewareTestSettings(t, "redis")
	b, _ := renderMiddleware(c)
	dir := filepath.Join(t.TempDir(), "owned")
	if err := saveMiddlewareBundle(dir, c, b, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Fatal("render read or saved credentials")
	}
	if _, err := os.Stat(filepath.Join(dir, ".deployment-started")); !os.IsNotExist(err) {
		t.Fatal("render claimed ownership of a deployed Docker project")
	}
	secret := "TestPassword12345678"
	if err := saveMiddlewareBundle(dir, c, b, secret); err != nil {
		t.Fatal(err)
	}
	if err := saveMiddlewareBundle(dir, c, b, "DifferentPassword123"); err == nil {
		t.Fatal("silently rotated cluster password")
	}
	modified := middlewareTestSettings(t, "redis")
	modified.Redis.Image = "example.invalid/redis:other"
	changed, _ := renderMiddleware(modified)
	if err := saveMiddlewareBundle(dir, modified, changed, secret); err == nil {
		t.Fatal("silently changed an existing deployment")
	}
	for name, data := range b.Files {
		if strings.Contains(string(data), secret) {
			t.Fatal("secret leaked into public artifact", name)
		}
	}
}

type middlewareProbeRemote struct {
	fail    bool
	uploads int
	writes  int
}

func (r *middlewareProbeRemote) Close() error { return nil }
func (r *middlewareProbeRemote) Upload(context.Context, string, io.Reader) error {
	r.uploads++
	return nil
}
func (r *middlewareProbeRemote) Exec(_ context.Context, _ bool, command string) ([]byte, error) {
	if strings.Contains(command, "uname -s") {
		if r.fail {
			return nil, errors.New("probe failed")
		}
		return []byte("ready"), nil
	}
	r.writes++
	return nil, nil
}

func TestMiddlewareSSHPreflightStopsBeforeWrites(t *testing.T) {
	c := middlewareTestSettings(t, "redis")
	c.Driver = "ssh"
	for i, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		c.Hosts = append(c.Hosts, clusterHost{Name: []string{"db1", "db2", "db3"}[i], Address: ip, InternalAddress: ip, Port: 22, User: "root"})
	}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	b, _ := renderMiddleware(c)
	if !strings.Contains(b.Bootstrap, "192.0.2.1:6380") || !strings.Contains(b.Bootstrap, "192.0.2.2:6379") {
		t.Fatal("cross-host Redis replica topology missing")
	}
	var remotes []*middlewareProbeRemote
	err := executeMiddlewareSSH(context.Background(), c, b, "deploy", io.Discard, func(_ context.Context, h clusterHost) (clusterRemote, error) {
		r := &middlewareProbeRemote{fail: h.Name == "db2"}
		remotes = append(remotes, r)
		return r, nil
	})
	if err == nil {
		t.Fatal("failed host preflight accepted")
	}
	for _, r := range remotes {
		if r.uploads != 0 || r.writes != 0 {
			t.Fatal("wrote configuration before all-host preflight passed")
		}
	}
	filtered, err := middlewareHostBundle(c, b, 0)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(filtered.Files["compose.yaml"], &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Services["redis1"] == nil || doc.Services["redis4"] == nil || doc.Services["redis2"] != nil {
		t.Fatal("incorrect host placement", doc.Services)
	}
	c.Hosts[1].InternalAddress = c.Hosts[0].InternalAddress
	if c.validate() == nil {
		t.Fatal("duplicate failure-domain address accepted")
	}
}
