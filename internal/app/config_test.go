package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/sxxpqp/opskit"
)

func TestConfigStrictValidation(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"unknown", "schema_version: 1\netcd:\n  endpont: localhost\n"},
		{"duplicate", "schema_version: 1\nschema_version: 1\n"},
		{"duration", "schema_version: 1\ntimeout: tomorrow\n"},
		{"schema", "schema_version: 2\n"},
		{"documents", "schema_version: 1\n---\nschema_version: 1\n"},
		{"type", "schema_version: 1\ns3:\n  path_style: perhaps\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(p, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			if err := loadConfig(p, true, pflag.NewFlagSet("test", pflag.ContinueOnError)); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
func TestConfigPrecedenceAndRelativePaths(t *testing.T) {
	base := t.TempDir()
	p := filepath.Join(base, "config.yaml")
	body := "schema_version: 1\ntimeout: 2m\netcd:\n  directory: ./backups\n  cacert: ./certs/ca.pem\n  etcdctl: ./bin/etcdctl\ns3:\n  bucket: configured\n  path_style: true\n"
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	f := pflag.NewFlagSet("test", pflag.ContinueOnError)
	bucket := f.String("bucket", "", "bucket")
	style := f.Bool("path-style", true, "style")
	dir := f.String("directory", "", "dir")
	ca := f.String("cacert", "", "ca")
	binary := f.String("etcdctl", "", "bin")
	if err := f.Parse([]string{"--bucket=cli", "--path-style=false"}); err != nil {
		t.Fatal(err)
	}
	if err := loadConfig(p, true, f); err != nil {
		t.Fatal(err)
	}
	if *bucket != "cli" || *style || *dir != filepath.Join(base, "backups") || *ca != filepath.Join(base, "certs", "ca.pem") || *binary != filepath.Join(base, "bin", "etcdctl") {
		t.Fatalf("unexpected config: %s %v %s %s %s", *bucket, *style, *dir, *ca, *binary)
	}
	missing := filepath.Join(base, "missing.yaml")
	if err := loadConfig(missing, false, f); err != nil {
		t.Fatal(err)
	}
	if err := loadConfig(missing, true, f); err == nil {
		t.Fatal("explicit missing config accepted")
	}
}
func TestInitAndDefaultConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	var output bytes.Buffer
	if code, err := Run(context.Background(), []string{"config", "init"}, &output, &output, "test"); code != 0 || err != nil {
		t.Fatalf("%d %v", code, err)
	}
	original, err := os.ReadFile(defaultConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, opskit.ConfigTemplate) {
		t.Fatal("template differs")
	}
	if code, err := Run(context.Background(), []string{"config", "init"}, &output, &output, "test"); code == 0 || err == nil {
		t.Fatal("existing config overwritten")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPSKIT_FAKE_ETCD", "1")
	// With only the executable override, backup settings must come from cwd/config/config.yaml.
	if code, err := Run(context.Background(), []string{"etcd", "backup", "--etcdctl", exe}, &output, &output, "test"); code != 0 || err != nil {
		t.Fatalf("%d %v %s", code, err, output.String())
	}
	files, _ := filepath.Glob(filepath.Join("data", "backups", "etcd", "*", "manifest.json"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	// Invalid default config fails before export; help remains available.
	if err = os.WriteFile(defaultConfigPath, []byte("unknown: value"), 0600); err != nil {
		t.Fatal(err)
	}
	if code, err := Run(context.Background(), []string{"etcd", "backup"}, &output, &output, "test"); code != 2 || err == nil {
		t.Fatal("invalid default config ignored")
	}
	if code, err := Run(context.Background(), []string{"etcd", "backup", "--help"}, &output, &output, "test"); code != 0 || err != nil {
		t.Fatal("help requires valid config")
	}
}

func TestProviderSelection(t *testing.T) {
	for _, provider := range []string{"minio", "aws", "custom"} {
		t.Run(provider, func(t *testing.T) {
			body := "schema_version: 1\ns3:\n  provider: " + provider + "\n  minio:\n    bucket: minio-bucket\n    endpoint: https://minio.example.com\n    access_key: minio-access\n    secret_key: minio-secret\n  aws:\n    bucket: aws-bucket\n  custom:\n    bucket: custom-bucket\n    endpoint: https://custom.example.com\n"
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			f := pflag.NewFlagSet("test", pflag.ContinueOnError)
			var s storage
			s.flags(f)
			if err := loadConfig(path, true, f, &s); err != nil {
				t.Fatal(err)
			}
			if s.bucket != provider+"-bucket" || s.region != "us-east-1" || s.pathStyle != (provider != "aws") {
				t.Fatalf("wrong provider values: bucket=%s region=%s pathStyle=%v", s.bucket, s.region, s.pathStyle)
			}
			if provider == "minio" {
				if s.accessKey != "minio-access" || s.secretKey != "minio-secret" {
					t.Fatal("selected credentials missing")
				}
			} else if s.accessKey != "" || s.secretKey != "" {
				t.Fatal("unselected credentials leaked")
			}
			if err := f.Parse([]string{"--bucket=override", "--path-style=false"}); err != nil {
				t.Fatal(err)
			}
			if err := loadConfig(path, true, f, &s); err != nil {
				t.Fatal(err)
			}
			if s.bucket != "override" || s.pathStyle {
				t.Fatal("CLI overrides ignored")
			}
		})
	}
}
func TestProviderErrors(t *testing.T) {
	for _, body := range []string{
		"s3:\n  provider: typo\n",
		"s3:\n  provider: minio\n",
		"s3:\n  minio: {}\n",
		"s3:\n  provider: minio\n  bucket: mixed\n  minio: {}\n",
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		os.WriteFile(path, []byte("schema_version: 1\n"+body), 0600)
		if err := loadConfig(path, true, pflag.NewFlagSet("test", pflag.ContinueOnError)); err == nil {
			t.Fatal("invalid provider configuration accepted")
		}
	}
	for _, block := range []string{"bucket: backup", "bucket: backup\n    endpoint: https://example.com\n    access_key: incomplete"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		os.WriteFile(path, []byte("schema_version: 1\ns3:\n  provider: minio\n  minio:\n    "+block+"\n"), 0600)
		var out bytes.Buffer
		if code, err := Run(context.Background(), []string{"etcd", "backup", "--config", path}, &out, &out, "test"); code != 2 || err == nil {
			t.Fatalf("must fail before snapshot: %d %v", code, err)
		}
	}
}
