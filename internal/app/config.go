package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/sxxpqp/opskit"
	"gopkg.in/yaml.v3"
)

const defaultConfigPath = "config/config.yaml"

type fileConfig struct {
	Install  runtimeInstallSettings `yaml:"install"`
	Go       versionSetting         `yaml:"go"`
	Versions struct {
		Docker     string `yaml:"docker"`
		CRIDockerd string `yaml:"cri_dockerd"`
		Containerd string `yaml:"containerd"`
		Runc       string `yaml:"runc"`
		CNIPlugins string `yaml:"cni_plugins"`
		CRITools   string `yaml:"cri_tools"`
		Kind       string `yaml:"kind"`
		Kubernetes string `yaml:"kubernetes"`
		Kubectl    string `yaml:"kubectl"`
		KubeSphere string `yaml:"kubesphere"`
	} `yaml:"-"`
	Kind struct {
		Version     string         `yaml:"version"`
		Kubernetes  versionSetting `yaml:"kubernetes"`
		KubeSphere  sphereSettings `yaml:"kubesphere"`
		Name        string         `yaml:"name"`
		Workers     *int           `yaml:"workers"`
		Kubeconfig  string         `yaml:"kubeconfig"`
		Binary      string         `yaml:"binary"`
		ConsolePort int            `yaml:"console_port"`
		HTTPPort    int            `yaml:"http_port"`
		HTTPSPort   int            `yaml:"https_port"`
	} `yaml:"kind"`
	K8s        clusterSettings `yaml:"k8s"`
	Kubeadm    kubeadmSettings `yaml:"-"`
	Mirrors    mirrorSettings  `yaml:"mirrors"`
	KubeSphere struct {
		StorageClass    string `yaml:"storage_class"`
		InstallerSource string `yaml:"installer_source"`
		Password        string `yaml:"password"`
	} `yaml:"-"`

	SchemaVersion int                 `yaml:"schema_version"`
	Timeout       string              `yaml:"timeout"`
	MySQL         mysqlBackupSettings `yaml:"mysql"`
	OpenEBS       openEBSSettings     `yaml:"openebs"`
	Middleware    middlewareSettings  `yaml:"middleware"`
	KubeBlocks    kubeBlocksSettings  `yaml:"kubeblocks"`
	Backup        struct {
		Retention struct {
			Keep      *int   `yaml:"keep"`
			OlderThan string `yaml:"older_than"`
		} `yaml:"retention"`
	} `yaml:"backup"`
	Etcd struct {
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

func readFileConfig(path string, required bool) (fileConfig, error) {
	var c fileConfig
	if path == defaultConfigPath && !required {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			if _, oldErr := os.Stat("opskit.yaml"); oldErr == nil {
				path = "opskit.yaml"
			}
		}
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !required {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("read config %s: %w", path, err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err = decoder.Decode(&c); err != nil {
		return c, fmt.Errorf("invalid config %s: %w", path, err)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return c, errors.New("config must contain exactly one YAML document")
	}
	if c.SchemaVersion != 1 {
		return c, errors.New("config schema_version must be 1")
	}

	c.projectComponents()
	return c, nil
}

func loadConfig(path string, required bool, f *pflag.FlagSet, stores ...*storage) error {
	c, err := readFileConfig(path, required)
	if err != nil {
		return err
	}
	// Legacy fallback paths are relative to the selected legacy file's directory (cwd).
	if path == defaultConfigPath && !required {
		if _, e := os.Stat(path); errors.Is(e, os.ErrNotExist) {
			path = "opskit.yaml"
		}
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
	if f.Lookup("mysqldump") != nil {
		// MySQL and etcd directory/TLS flags must never borrow each other's settings.
		for _, name := range []string{"endpoint", "directory", "cacert", "cert", "key-file"} {
			delete(values, name)
		}
		binaryPath := func(v *string) *string {
			if v != nil && strings.ContainsAny(*v, "/\\") {
				return resolve(v)
			}
			return v
		}
		for name, value := range map[string]*string{
			"client-mode": c.MySQL.ClientMode, "image": c.MySQL.Docker.Image, "docker-bin": binaryPath(c.MySQL.Docker.Binary), "docker-network": c.MySQL.Docker.Network, "pull-policy": c.MySQL.Docker.PullPolicy,
			"host": c.MySQL.Host, "user": c.MySQL.User, "password-env": c.MySQL.PasswordEnv,
			"directory": resolve(c.MySQL.Directory), "mysqldump": binaryPath(c.MySQL.Dump), "mysql-client": binaryPath(c.MySQL.Client),
			"ssl-mode": c.MySQL.SSLMode, "ssl-ca": resolve(c.MySQL.SSLCA), "ssl-cert": resolve(c.MySQL.SSLCert), "ssl-key": resolve(c.MySQL.SSLKey),
		} {
			values[name] = value
		}
		setValue := func(name, value string) error {
			if !f.Changed(name) {
				return f.Set(name, value)
			}
			return nil
		}
		if c.MySQL.Port != nil {
			if err := setValue("port", strconv.Itoa(*c.MySQL.Port)); err != nil {
				return err
			}
		}
		for name, value := range map[string]*bool{"gzip": c.MySQL.Gzip, "routines": c.MySQL.Routines, "events": c.MySQL.Events, "triggers": c.MySQL.Triggers, "allow-non-transactional": c.MySQL.AllowNonTransactional} {
			if value != nil {
				if err := setValue(name, strconv.FormatBool(*value)); err != nil {
					return err
				}
			}
		}
		if c.MySQL.Databases != nil && !f.Changed("database") {
			if err := f.Lookup("database").Value.(pflag.SliceValue).Replace(c.MySQL.Databases); err != nil {
				return err
			}
		}
	}
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
	f.Visit(func(v *pflag.Flag) { explicit[v.Name] = true })
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

func newConfigInitCommand(out, diag io.Writer) (*cobra.Command, error) {
	cmd := &cobra.Command{Use: "init"}
	f := cmd.Flags()
	path := f.String("config", defaultConfigPath, "template destination (never overwrites existing files)")
	bindCommand(cmd, func() (int, error) {
		if f.NArg() != 0 {
			return 2, errors.New("unexpected positional arguments")
		}
		if *path == defaultConfigPath && !explicitFlag(f, "config") {
			if _, err := os.Stat("opskit.yaml"); err == nil {
				return 1, errors.New("已存在旧 opskit.yaml；请迁移旧文件并核对相对路径，或显式 --config 指定模板目标，不自动创建第二套配置")
			}
		}
		if err := os.MkdirAll(filepath.Dir(*path), 0700); err != nil {
			return 1, err
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
	})
	return cmd, nil
}

func defaultInstallSettings() (runtimeInstallSettings, error) {
	var c fileConfig
	err := yaml.Unmarshal(opskit.ConfigTemplate, &c)
	return c.Install, err
}
