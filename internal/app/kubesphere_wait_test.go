package app

import (
	"context"
	"errors"
	templates "github.com/sxxpqp/opskit"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWaitKubeSphereCoreMissingStatus(t *testing.T) {
	responses := []string{`{}`, `{"status":{}}`, `{"status":{"core":{"status":"installing"}}}`, `{"status":{"core":{"status":"enabled"}}}`}
	calls := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := waitKubeSphereCore(ctx, time.Nanosecond, func(context.Context) ([]byte, error) {
		raw := responses[calls]
		calls++
		return []byte(raw), nil
	})
	if err != nil || calls != len(responses) {
		t.Fatal(calls, err)
	}
}

func TestWaitKubeSphereCoreFailures(t *testing.T) {
	expected := errors.New("forbidden")
	err := waitKubeSphereCore(context.Background(), time.Nanosecond, func(context.Context) ([]byte, error) { return nil, expected })
	if !errors.Is(err, expected) {
		t.Fatal(err)
	}
	err = waitKubeSphereCore(context.Background(), time.Nanosecond, func(context.Context) ([]byte, error) { return []byte("invalid"), nil })
	if err == nil {
		t.Fatal("invalid JSON accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	err = waitKubeSphereCore(ctx, time.Hour, func(context.Context) ([]byte, error) { cancel(); return []byte(`{}`), nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestKubeSpherePasswordConfiguration(t *testing.T) {
	var defaults fileConfig
	if err := yaml.Unmarshal(templates.ConfigTemplate, &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.KubeSphere.Password != "sxxpqp.top" {
		t.Fatal("incorrect default password")
	}
	_, err := readFileConfig(writeKubeSphereLegacyConfig(t), true)
	if err == nil {
		t.Fatal("removed password_file accepted")
	}
	code, err, _ := workflowRun(t, "kubesphere", "install", "--password-file", "old.secret")
	if code == 0 || err == nil {
		t.Fatal("removed --password-file accepted")
	}
}

func writeKubeSphereLegacyConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("schema_version: 1\nkubesphere:\n  password_file: old.secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
