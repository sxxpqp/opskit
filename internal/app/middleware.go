package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	templates "github.com/sxxpqp/opskit"
	"gopkg.in/yaml.v3"
)

type middlewareEngineSettings struct {
	Image         string `yaml:"image,omitempty"`
	ShellImage    string `yaml:"shell_image,omitempty"`
	RouterImage   string `yaml:"router_image,omitempty"`
	FrontendImage string `yaml:"frontend_image,omitempty"`
	PasswordEnv   string `yaml:"password_env,omitempty"`
	Username      string `yaml:"username,omitempty"`
}
type middlewareSettings struct {
	Name          string                   `yaml:"name,omitempty"`
	Engine        string                   `yaml:"engine,omitempty"`
	Driver        string                   `yaml:"driver,omitempty"`
	Directory     string                   `yaml:"directory,omitempty"`
	DockerContext string                   `yaml:"docker_context,omitempty"`
	BindAddress   string                   `yaml:"bind_address,omitempty"`
	PublishPort   int                      `yaml:"publish_port,omitempty"`
	Hosts         []clusterHost            `yaml:"hosts,omitempty"`
	MySQL         middlewareEngineSettings `yaml:"mysql,omitempty"`
	Redis         middlewareEngineSettings `yaml:"redis,omitempty"`
	MinIO         middlewareEngineSettings `yaml:"minio,omitempty"`
}

type middlewareBundle struct {
	Files     map[string][]byte
	Nodes     []map[string]any
	Bootstrap string
	Check     string
	Recovery  string
}

func middlewareDefaults(configured middlewareSettings) (middlewareSettings, error) {
	var defaults struct {
		Middleware middlewareSettings `yaml:"middleware"`
	}
	if err := yaml.Unmarshal(templates.ConfigTemplate, &defaults); err != nil {
		return defaults.Middleware, err
	}
	raw, err := yaml.Marshal(configured)
	if err != nil {
		return defaults.Middleware, err
	}
	err = yaml.Unmarshal(raw, &defaults.Middleware)
	return defaults.Middleware, err
}

func (c middlewareSettings) project() string { return c.Name + "-" + c.Engine }
func (c middlewareSettings) component() middlewareEngineSettings {
	switch c.Engine {
	case "mysql":
		return c.MySQL
	case "redis":
		return c.Redis
	default:
		return c.MinIO
	}
}
func (c *middlewareSettings) validate() error {
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`).MatchString(c.Name) {
		return errors.New("middleware.name 须为 1～40 位小写字母、数字或连字符，以字母开头")
	}
	if c.Engine != "mysql" && c.Engine != "redis" && c.Engine != "minio" {
		return errors.New("engine 必须是 mysql/redis/minio")
	}
	if c.Driver != "docker" && c.Driver != "ssh" {
		return errors.New("driver 必须是 docker（单机容器验证）或 ssh（多 Linux 主机）")
	}
	component := c.component()
	if component.Image == "" || component.PasswordEnv == "" {
		return errors.New("必须配置所选组件 image 和 password_env")
	}
	if c.Engine == "mysql" && (component.ShellImage == "" || component.RouterImage == "") {
		return errors.New("MySQL 需要 shell_image 和 router_image")
	}
	if c.Engine == "minio" && !regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{2,31}$`).MatchString(component.Username) {
		return errors.New("MinIO username 必须为 3～32 位安全账号名")
	}
	if c.Engine == "minio" && component.FrontendImage == "" {
		return errors.New("MinIO 需要 frontend_image 提供 HAProxy 入口")
	}
	if c.PublishPort < 0 || c.PublishPort > 65535 || net.ParseIP(c.BindAddress) == nil {
		return errors.New("publish_port 或 bind_address 无效")
	}
	if c.Driver == "docker" {
		if len(c.Hosts) > 0 {
			return errors.New("docker 验证模式不读取 hosts；多主机请使用 driver: ssh")
		}
		return nil
	}
	expected := 3
	if c.Engine == "minio" {
		expected = 4
	}
	if len(c.Hosts) != expected {
		return fmt.Errorf("%s 的 ssh 部署要求 %d 台不同 Linux 主机", c.Engine, expected)
	}
	seen := map[string]bool{}
	for i := range c.Hosts {
		h := &c.Hosts[i]
		if h.InternalAddress == "" {
			h.InternalAddress = h.Address
		}
		if h.Port == 0 {
			h.Port = 22
		}
		if h.Address == "" || h.User == "" || h.Port < 1 || h.Port > 65535 || net.ParseIP(h.InternalAddress) == nil || strings.Contains(h.InternalAddress, ":") || seen[h.InternalAddress] {
			return errors.New("hosts 需要互异 IPv4 internalAddress、SSH address/user 和有效端口")
		}
		seen[h.InternalAddress] = true
	}
	return nil
}

func middlewareDockerArgs(c middlewareSettings, directory string, args ...string) []string {
	prefix := []string{}
	if c.DockerContext != "" {
		prefix = append(prefix, "--context", c.DockerContext)
	}
	prefix = append(prefix, "compose", "--project-name", c.project(), "--project-directory", directory, "-f", filepath.Join(directory, "compose.yaml"))
	return append(prefix, args...)
}

// The private .env is authoritative; a host's unrelated generic variable must not override it.
func middlewareRun(ctx context.Context, c middlewareSettings, directory string, out, diag io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", middlewareDockerArgs(c, directory, args...)...)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, "OPSKIT_CLUSTER_PASSWORD") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Stdout = out
	cmd.Stderr = diag
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func newMiddlewareCommand(parent context.Context, action string, out, diag io.Writer) (*cobra.Command, error) {
	cmd := &cobra.Command{Use: action}
	f := cmd.Flags()
	config := f.String("config", defaultConfigPath, "统一 YAML 配置，读取 middleware")
	engine := f.String("engine", "", "mysql / redis / minio")
	driver := f.String("driver", "", "docker 单机容器验证 / ssh 多主机部署")
	directory := f.String("directory", "", "本地部署资料目录，默认 ~/.opskit/middleware/<名称>-<组件>")
	dockerContext := f.String("docker-context", "", "Docker CLI context，仅用于 docker 验证模式")
	dry := f.Bool("dry-run", false, "只检查配置并展示拓扑，不读取秘密、不连接或写文件")
	timeout := f.Duration("timeout", 20*time.Minute, "部署或状态检查总超时")
	bindCommand(cmd, func() (int, error) {
		if f.NArg() != 0 || *timeout <= 0 {
			return 2, errors.New("多余参数或 timeout 无效")
		}
		file, err := commandConfig(f, *config)
		if err != nil {
			return 2, err
		}
		c, err := middlewareDefaults(file.Middleware)
		if err != nil {
			return 2, err
		}
		if *engine != "" {
			c.Engine = *engine
		}
		if *driver != "" {
			c.Driver = *driver
		}
		if *dockerContext != "" {
			c.DockerContext = *dockerContext
		}
		if action == "recover" && c.Engine != "mysql" {
			return 2, errors.New("recover 当前只支持 MySQL 完整停机恢复")
		}
		if err := c.validate(); err != nil {
			return 2, err
		}
		if *directory == "" {
			*directory = c.Directory
		}
		if *directory == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return 1, err
			}
			*directory = filepath.Join(home, ".opskit", "middleware", c.project())
		}
		*directory, err = filepath.Abs(*directory)
		if err != nil {
			return 2, err
		}
		bundle, err := renderMiddleware(c)
		if err != nil {
			return 2, err
		}
		if *dry {
			return 0, json.NewEncoder(out).Encode(struct {
				Engine    string `json:"engine"`
				Driver    string `json:"driver"`
				Nodes     int    `json:"nodes"`
				Scope     string `json:"failure_scope"`
				Directory string `json:"directory"`
			}{c.Engine, c.Driver, len(bundle.Nodes), map[string]string{"docker": "process-only; one Docker Engine", "ssh": "separate Linux hosts; verify failure domains independently"}[c.Driver], *directory})
		}
		ctx, cancel := context.WithTimeout(parent, *timeout)
		defer cancel()
		if c.Driver == "docker" && action != "status" {
			if err := os.MkdirAll(filepath.Dir(*directory), 0700); err != nil {
				return 1, err
			}
			lockPath := *directory + ".deploy.lock"
			lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return 1, fmt.Errorf("部署锁存在或不可写，请确认没有任务运行: %s", lockPath)
			}
			lock.Close()
			defer os.Remove(lockPath)
		}
		if action == "render" {
			if c.Driver != "docker" {
				return 2, errors.New("ssh 模式请使用 deploy --dry-run 或 deploy，逐节点文件由部署器分发")
			}
			if err := saveMiddlewareBundle(*directory, c, bundle, ""); err != nil {
				return 1, err
			}
			fmt.Fprintln(out, "已生成部署资料；设置所选组件 password_env 后使用 middleware deploy:", *directory)
			return 0, nil
		}
		if c.Driver == "ssh" {
			return taskError(ctx, deployMiddlewareSSH(ctx, c, bundle, action, out))
		}
		if action == "status" {
			return taskError(ctx, middlewareDockerCheck(ctx, c, *directory, bundle, out, diag))
		}
		if action == "recover" {
			if c.Engine != "mysql" {
				return 2, errors.New("recover 当前只支持 MySQL 完整停机恢复")
			}
			if _, err := os.Stat(filepath.Join(*directory, "ownership.json")); err != nil {
				return 2, errors.New("缺少已有部署归属记录，不能执行恢复")
			}
			if _, err := os.Stat(filepath.Join(*directory, ".deployment-started")); err != nil {
				return 2, errors.New("目录仅有渲染资料，没有实际部署记录，不能恢复")
			}
			if err := saveMiddlewareBundle(*directory, c, bundle, ""); err != nil {
				return 1, err
			}
			if err := middlewareRun(ctx, c, *directory, out, diag, "run", "--rm", "--no-deps", "recover"); err != nil {
				return taskError(ctx, err)
			}
			if err := middlewareRun(ctx, c, *directory, out, diag, "up", "-d", "router"); err != nil {
				return taskError(ctx, err)
			}
			return taskError(ctx, middlewareDockerCheck(ctx, c, *directory, bundle, out, diag))
		}
		secret, err := middlewarePassword(c)
		if err != nil {
			return 2, err
		}
		if err := middlewareDockerOwnership(ctx, c, *directory); err != nil {
			return taskError(ctx, err)
		}
		if err := saveMiddlewareBundle(*directory, c, bundle, secret); err != nil {
			return 1, err
		}
		if err := middlewareRun(ctx, c, *directory, out, diag, "up", "-d", "--wait", "--wait-timeout", "600"); err != nil {
			return taskError(ctx, err)
		}
		if bundle.Bootstrap != "" {
			if err := middlewareRun(ctx, c, *directory, out, diag, "run", "--rm", "--no-deps", "bootstrap"); err != nil {
				return taskError(ctx, fmt.Errorf("集群初始化失败，保留容器和数据供排查: %w", err))
			}
		}
		if c.Engine == "mysql" {
			if err := middlewareRun(ctx, c, *directory, out, diag, "up", "-d", "router"); err != nil {
				return taskError(ctx, err)
			}
		}
		if c.Engine == "minio" {
			if err := middlewareRun(ctx, c, *directory, out, diag, "exec", "-T", "frontend", "haproxy", "-c", "-f", "/usr/local/etc/haproxy/haproxy.cfg"); err != nil {
				return taskError(ctx, err)
			}
			if err := middlewareRun(ctx, c, *directory, out, diag, "kill", "--signal", "USR2", "frontend"); err != nil {
				return taskError(ctx, err)
			}
		}
		return taskError(ctx, middlewareDockerCheck(ctx, c, *directory, bundle, out, diag))
	})
	return cmd, nil
}

func middlewareDockerCheck(ctx context.Context, c middlewareSettings, directory string, b middlewareBundle, out, diag io.Writer) error {
	if _, err := os.Stat(filepath.Join(directory, ".deployment-started")); err != nil {
		return errors.New("部署资料或 ownership.json 不存在，请先 deploy")
	}
	owner, err := os.ReadFile(filepath.Join(directory, "ownership.json"))
	if err != nil || string(owner) != string(middlewareIdentity(c)) {
		return errors.New("部署归属或参数与当前配置不一致，拒绝使用其他项目资料")
	}
	if err := middlewareRun(ctx, c, directory, out, diag, "ps", "--format", "json"); err != nil {
		return err
	}
	if err := middlewareRun(ctx, c, directory, out, diag, "run", "--rm", "--no-deps", "check"); err != nil {
		return fmt.Errorf("集群拓扑/健康检查未通过: %w", err)
	}
	fmt.Fprintf(out, "%s 拓扑和健康检查通过；模式=%s。\n", c.project(), c.Driver)
	return nil
}

func saveMiddlewareBundle(directory string, c middlewareSettings, b middlewareBundle, secret string) error {
	// A deployment name owns its artifacts; never adopt arbitrary compose directories.
	marker := filepath.Join(directory, "ownership.json")
	raw := middlewareIdentity(c)
	if info, err := os.Lstat(directory); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("部署目录不是普通目录")
		}
		old, err := os.ReadFile(marker)
		if err != nil || string(old) != string(raw) {
			return errors.New("部署目录无匹配的归属记录，拒绝覆盖")
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	if err := restrictMySQLDirectory(directory); err != nil {
		return fmt.Errorf("保护部署目录: %w", err)
	}
	if secret != "" {
		envPath := filepath.Join(directory, ".env")
		if old, err := os.ReadFile(envPath); err == nil && string(old) != "OPSKIT_CLUSTER_PASSWORD="+secret+"\n" {
			return errors.New("密码与现有部署不同；此命令不执行密码轮换")
		}
		if err := os.WriteFile(envPath, []byte("OPSKIT_CLUSTER_PASSWORD="+secret+"\n"), 0600); err != nil {
			return err
		}
	}
	for name, data := range b.Files {
		// Bind-mounted scripts/config contain references only; non-root tool containers must read them.
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, data, 0644); err != nil {
			return err
		}
		if err := os.Chmod(path, 0644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(marker, raw, 0600); err != nil {
		return err
	}
	if secret != "" {
		return os.WriteFile(filepath.Join(directory, ".deployment-started"), raw, 0600)
	}
	return nil
}

func middlewarePassword(c middlewareSettings) (string, error) {
	secret, ok := os.LookupEnv(c.component().PasswordEnv)
	if !ok || !regexp.MustCompile(`^[a-zA-Z0-9._@%+=:-]{16,128}$`).MatchString(secret) {
		return "", errors.New("所选 password_env 必须设置 16～128 位字母、数字或 ._@%+=:- 密码")
	}
	return secret, nil
}

func middlewareDockerOwnership(ctx context.Context, c middlewareSettings, directory string) error {
	prefix := []string{}
	if c.DockerContext != "" {
		prefix = append(prefix, "--context", c.DockerContext)
	}
	if _, err := commandOutput(ctx, nil, "docker", append(append([]string{}, prefix...), "info")...); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(directory, ".deployment-started")); err == nil {
		return nil
	}
	for _, args := range [][]string{{"ps", "-aq", "--filter", "label=com.docker.compose.project=" + c.project()}, {"volume", "ls", "-q", "--filter", "name=^" + c.project() + "_"}} {
		raw, err := commandOutput(ctx, nil, "docker", append(append([]string{}, prefix...), args...)...)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(raw)) != "" {
			return errors.New("同名 Docker 项目已有资源，但本地无归属记录；拒绝接管")
		}
	}
	return nil
}
