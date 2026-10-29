package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

type diagnosticItem struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
}

func newDoctorCommand(parent context.Context, out io.Writer) (*cobra.Command, error) {
	cmd := &cobra.Command{Use: "doctor", Short: "检查本机配置、依赖和下载目录，给出修复指引"}
	f := cmd.Flags()
	config := f.String("config", defaultConfigPath, "统一 YAML 配置")
	profile := f.String("profile", "local", "检查场景：local / kind / k8s / backup")
	format := f.String("format", "text", "输出 text / json")
	timeout := f.Duration("timeout", 20*time.Second, "检查总超时")
	network := f.Bool("network", false, "额外检查配置的下载根地址是否可达；HEAD 不证明目标文件存在")
	bindCommand(cmd, func() (int, error) {
		if f.NArg() != 0 || *timeout <= 0 || (*format != "text" && *format != "json") {
			return 2, errors.New("参数无效")
		}
		tools := map[string][]string{"local": {}, "kind": {"kind", "kubectl", "docker"}, "k8s": {"kubectl"}, "backup": {"etcdctl"}}
		names, ok := tools[*profile]
		if !ok {
			return 2, errors.New("profile 必须是 local/kind/k8s/backup")
		}
		items := []diagnosticItem{}
		c, err := commandConfig(f, *config)
		configOK := err == nil
		if err != nil {
			items = append(items, diagnosticItem{"config", "failed", "配置读取或解析失败", "检查 --config 文件及 YAML 字段"})
		} else {
			items = append(items, diagnosticItem{"config", "ok", "配置可读取，或使用内置默认值", ""})
		}
		ctx, cancel := context.WithTimeout(parent, *timeout)
		defer cancel()
		for _, name := range names {
			binary := name
			if name == "kind" {
				binary = c.Kind.Binary
				if binary == "" {
					binary = "kind"
				}
				binary = configuredKindBinary(c, binary)
			}
			if name == "etcdctl" && c.Etcd.Binary != nil {
				binary = *c.Etcd.Binary
			}
			path, lookupErr := exec.LookPath(binary)
			item := diagnosticItem{name, "ok", path, ""}
			if lookupErr != nil {
				item.Status, item.Detail = "failed", "未找到可执行文件: "+binary
				switch name {
				case "kind":
					item.Hint = "opskit kind install 或修正 kind.binary"
				case "kubectl":
					item.Hint = kindKubectlInstallHint(c.Kind.Kubernetes.Version)
				case "docker":
					item.Hint = "安装并启动 Docker，再执行 docker info"
				case "etcdctl":
					item.Hint = "opskit k8s download --components etcd --etcd-version vX.Y.Z，安装客户端后配置 etcd.etcdctl"
				}
			} else if name == "docker" {
				if _, err := commandOutput(ctx, nil, binary, "info"); err != nil {
					item.Status, item.Detail, item.Hint = "failed", "Docker Engine 不可用", "启动服务并确认当前用户有权运行 docker info"
				}
			}
			items = append(items, item)
		}
		err = nil
		directory := c.Install.DownloadDir
		if directory == "" {
			directory, err = defaultDownloadDir()
		}
		if err == nil {
			probe := directory
			for {
				if _, statErr := os.Stat(probe); statErr == nil {
					break
				} else if !os.IsNotExist(statErr) {
					err = statErr
					break
				}
				parent := filepath.Dir(probe)
				if parent == probe {
					err = errors.New("找不到可写父目录")
					break
				}
				probe = parent
			}
			if err == nil {
				file, probeErr := os.CreateTemp(probe, ".opskit-doctor-*")
				err = probeErr
				if file != nil {
					file.Close()
					os.Remove(file.Name())
				}
			}
		}
		item := diagnosticItem{"download-directory", "ok", directory, ""}
		if err != nil {
			item.Status, item.Hint = "failed", "修正 install.download_dir 并确认目录权限"
		}
		items = append(items, item)
		if *profile == "k8s" {
			item := diagnosticItem{"cluster-config", "ok", "节点与版本配置有效", ""}
			if err := validateCluster(&c.K8s); err != nil {
				item.Status, item.Detail, item.Hint = "failed", err.Error(), "修正 k8s 配置后执行 opskit k8s check 检查 SSH 目标"
			}
			items = append(items, item)
			output := c.K8s.Deploy.OutputDir
			if output == "" {
				if home, err := os.UserHomeDir(); err == nil {
					output = filepath.Join(home, ".opskit", "clusters", c.K8s.Name)
				}
			}
			if _, err := os.Stat(filepath.Join(output, "deploy.lock")); err == nil {
				items = append(items, diagnosticItem{"deploy-lock", "failed", filepath.Join(output, "deploy.lock"), "确认原部署进程已退出后再手动删除锁；不要在部署运行时移除"})
			}
		}
		if *network && configOK {
			defaults, err := defaultInstallSettings()
			if err != nil {
				return 1, err
			}
			for _, source := range []struct{ name, configured, fallback string }{
				{"kubectl", c.Install.Kubectl.DownloadBase, defaults.Kubectl.DownloadBase},
				{"kind", c.Install.Kind.DownloadBase, defaults.Kind.DownloadBase},
				{"containerd", c.Install.Containerd.DownloadBase, defaults.Containerd.DownloadBase},
				{"go", c.Install.Go.DownloadBase, defaults.Go.DownloadBase},
			} {
				base := source.configured
				if base == "" {
					base = source.fallback
				}
				item := diagnosticItem{"download-source/" + source.name, "ok", "根地址可达（不验证具体文件或下载速度）", ""}
				u, err := url.Parse(base)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
					item.Status, item.Detail, item.Hint = "failed", "下载根地址无效", "修正 install 中对应的 download_base"
				} else {
					request, err := http.NewRequestWithContext(ctx, http.MethodHead, base, nil)
					if err == nil {
						response, requestErr := http.DefaultClient.Do(request)
						err = requestErr
						if response != nil {
							response.Body.Close()
							if response.StatusCode >= 500 || response.StatusCode == 401 || response.StatusCode == 403 {
								err = fmt.Errorf("HTTP %d", response.StatusCode)
							}
						}
					}
					if err != nil {
						item.Status, item.Detail, item.Hint = "failed", "下载根地址不可达或拒绝访问", "检查 install 下载源、代理、证书与网络；HEAD 不下载文件"
					}
				}
				items = append(items, item)
			}
		}
		failed := false
		for _, item := range items {
			failed = failed || item.Status == "failed"
		}
		if *format == "json" {
			if err := json.NewEncoder(out).Encode(struct {
				SchemaVersion int              `json:"schema_version"`
				Profile       string           `json:"profile"`
				OK            bool             `json:"ok"`
				Checks        []diagnosticItem `json:"checks"`
			}{1, *profile, !failed, items}); err != nil {
				return 1, err
			}
		} else {
			for _, item := range items {
				fmt.Fprintf(out, "[%s] %s: %s\n", item.Status, item.Name, item.Detail)
				if item.Hint != "" {
					fmt.Fprintln(out, "  建议:", item.Hint)
				}
			}
		}
		if failed {
			return 3, errors.New("环境诊断未通过，请按指引处理失败项")
		}
		return 0, nil
	})
	return cmd, nil
}
