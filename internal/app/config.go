package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sxxpqp/opskit"
	"gopkg.in/yaml.v3"
)

type fileConfig struct {
	SchemaVersion int    `yaml:"schema_version"`
	Timeout       string `yaml:"timeout"`
	Etcd          struct {
		Endpoint  *string `yaml:"endpoint"`
		Directory *string `yaml:"directory"`
		Binary    *string `yaml:"etcdctl"`
		CA        *string `yaml:"cacert"`
		Cert      *string `yaml:"cert"`
		Key       *string `yaml:"key_file"`
	} `yaml:"etcd"`
	S3 struct {
		providerConfig `yaml:",inline"`
		Provider       string          `yaml:"provider"`
		MinIO          *providerConfig `yaml:"minio"`
		AWS            *providerConfig `yaml:"aws"`
		Custom         *providerConfig `yaml:"custom"`
	} `yaml:"s3"`
}

type providerConfig struct {
	Bucket       *string `yaml:"bucket"`
	Endpoint     *string `yaml:"endpoint"`
	Region       *string `yaml:"region"`
	PathStyle    *bool   `yaml:"path_style"`
	Key          *string `yaml:"key"`
	AccessKey    string  `yaml:"access_key"`
	SecretKey    string  `yaml:"secret_key"`
	SessionToken string  `yaml:"session_token"`
}

func (c *fileConfig) selectStorage() (providerConfig, error) {
	legacy := c.S3.providerConfig
	hasFlat := legacy.Bucket != nil || legacy.Endpoint != nil || legacy.Region != nil || legacy.PathStyle != nil || legacy.Key != nil || legacy.AccessKey != "" || legacy.SecretKey != "" || legacy.SessionToken != ""
	if c.S3.Provider == "" {
		if c.S3.MinIO != nil || c.S3.AWS != nil || c.S3.Custom != nil {
			return providerConfig{}, errors.New("s3.provider is required when provider blocks are configured")
		}
		return legacy, nil
	}
	if hasFlat {
		return providerConfig{}, errors.New("move flat s3 fields into the selected provider block")
	}
	var selected *providerConfig
	switch c.S3.Provider {
	case "minio":
		selected = c.S3.MinIO
	case "aws":
		selected = c.S3.AWS
	case "custom":
		selected = c.S3.Custom
	default:
		return providerConfig{}, errors.New("s3.provider must be minio, aws or custom")
	}
	if selected == nil {
		return providerConfig{}, fmt.Errorf("s3.%s block is required", c.S3.Provider)
	}
	result := *selected
	if result.Region == nil {
		v := "us-east-1"
		result.Region = &v
	}
	if result.PathStyle == nil {
		v := c.S3.Provider != "aws"
		result.PathStyle = &v
	}
	return result, nil
}

func loadConfig(path string, required bool, f *flag.FlagSet, stores ...*storage) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !required {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	var c fileConfig
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err = decoder.Decode(&c); err != nil {
		return fmt.Errorf("invalid config %s: %w", path, err)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return errors.New("config must contain exactly one YAML document")
	}
	if c.SchemaVersion != 1 {
		return errors.New("config schema_version must be 1")
	}
	if c.Timeout != "" {
		d, e := time.ParseDuration(c.Timeout)
		if e != nil || d <= 0 {
			return errors.New("config timeout must be a positive duration")
		}
	}
	selected, err := c.selectStorage()
	if err != nil {
		return err
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return err
	}
	resolve := func(v *string) *string {
		if v == nil || *v == "" || filepath.IsAbs(*v) {
			return v
		}
		s := filepath.Join(base, *v)
		return &s
	}
	values := map[string]*string{"timeout": nil, "endpoint": c.Etcd.Endpoint, "directory": resolve(c.Etcd.Directory), "cacert": resolve(c.Etcd.CA), "cert": resolve(c.Etcd.Cert), "key-file": resolve(c.Etcd.Key), "bucket": selected.Bucket, "s3-endpoint": selected.Endpoint, "region": selected.Region, "key": selected.Key}
	if c.Timeout != "" {
		values["timeout"] = &c.Timeout
	}
	if c.Etcd.Binary != nil {
		v := c.Etcd.Binary
		if strings.ContainsAny(*v, "/\\") {
			v = resolve(v)
		}
		values["etcdctl"] = v
	}
	if selected.PathStyle != nil {
		v := strconv.FormatBool(*selected.PathStyle)
		values["path-style"] = &v
	}
	explicit := map[string]bool{}
	f.Visit(func(v *flag.Flag) { explicit[v.Name] = true })
	for name, value := range values {
		if value != nil && !explicit[name] && f.Lookup(name) != nil {
			if err = f.Set(name, *value); err != nil {
				return fmt.Errorf("config field %s: %w", name, err)
			}
		}
	}
	for _, s := range stores {
		s.provider = c.S3.Provider
		s.accessKey = selected.AccessKey
		s.secretKey = selected.SecretKey
		s.sessionToken = selected.SessionToken
	}
	return nil
}

func initConfig(args []string, out, diag io.Writer) (int, error) {
	f := flag.NewFlagSet("config init", flag.ContinueOnError)
	f.SetOutput(diag)
	path := f.String("config", "opskit.yaml", "template destination (never overwrites existing files)")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	if f.NArg() != 0 {
		return 2, errors.New("unexpected positional arguments")
	}
	file, err := os.OpenFile(*path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 1, err
	}
	if _, err = file.Write(opskit.ConfigTemplate); err != nil {
		file.Close()
		return 1, err
	}
	if err = file.Close(); err != nil {
		return 1, err
	}
	fmt.Fprintln(out, "Created", *path)
	return 0, nil
}
