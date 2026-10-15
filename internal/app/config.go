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
		Bucket    *string `yaml:"bucket"`
		Endpoint  *string `yaml:"endpoint"`
		Region    *string `yaml:"region"`
		PathStyle *bool   `yaml:"path_style"`
		Key       *string `yaml:"key"`
	} `yaml:"s3"`
}

func loadConfig(path string, required bool, f *flag.FlagSet) error {
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
	values := map[string]*string{"timeout": nil, "endpoint": c.Etcd.Endpoint, "directory": resolve(c.Etcd.Directory), "cacert": resolve(c.Etcd.CA), "cert": resolve(c.Etcd.Cert), "key-file": resolve(c.Etcd.Key), "bucket": c.S3.Bucket, "s3-endpoint": c.S3.Endpoint, "region": c.S3.Region, "key": c.S3.Key}
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
	if c.S3.PathStyle != nil {
		v := strconv.FormatBool(*c.S3.PathStyle)
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
