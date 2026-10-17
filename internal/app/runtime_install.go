package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

type runtimeInstallSettings struct {
	DownloadDir      string `yaml:"download_dir"`
	DockerMethod     string `yaml:"docker_method"`
	DockerMirror     string `yaml:"docker_mirror"`
	DockerScript     string `yaml:"docker_script"`
	DockerBinaryBase string `yaml:"docker_binary_base"`
	ReleaseBase      string `yaml:"release_base"`
}

type runtimePackage struct {
	name, version, source, checksum string
	raw                             bool
	files                           map[string][]byte
}

func runRuntimeInstall(parent context.Context, tool string, args []string, out, diag io.Writer) (int, error) {
	f := flag.NewFlagSet(tool+" install", flag.ContinueOnError)
	f.SetOutput(diag)
	downloadDir := f.String("download-dir", "", "统一下载目录，默认 ./downloads")
	config := f.String("config", defaultConfigPath, "统一配置文件")
	version := f.String("version", "", "目标版本 vX.Y.Z；也可由 versions 配置")
	dryRun := f.Bool("dry-run", false, "仅预览安装计划，不执行安装")
	arch := f.String("arch", runtime.GOARCH, "Linux 目标架构 amd64/arm64")
	source := f.String("url", "", "覆盖主安装包或 Docker 官方脚本 URL")
	checksum := f.String("sha256", "", "主安装包 SHA-256；指定后强制校验")
	timeout := f.Duration("timeout", 15*time.Minute, "安装总超时")
	var method, mirror, runc, cni *string
	if tool == "docker" {
		method = f.String("method", "script", "script 或 binary")
		mirror = f.String("mirror", "Aliyun", "官方脚本软件包源 Aliyun 或 AzureChinaCloud")
	}
	if tool == "containerd" {
		runc = f.String("runc-version", "v1.2.6", "配套 runc 版本")
		cni = f.String("cni-version", "v1.5.1", "CNI plugins 版本")
	}
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	if f.NArg() != 0 {
		return 2, errors.New("不接受位置参数")
	}
	c, err := commandConfig(f, *config)
	if err != nil {
		return 1, err
	}
	setConfigFlag(f, "download-dir", c.Install.DownloadDir)
	if *downloadDir == "" {
		*downloadDir, err = defaultDownloadDir()
		if err != nil {
			return 1, err
		}
	}
	switch tool {
	case "docker":
		setConfigFlag(f, "version", c.Versions.Docker)
		setConfigFlag(f, "method", c.Install.DockerMethod)
		setConfigFlag(f, "mirror", c.Install.DockerMirror)
	case "cri-dockerd":
		setConfigFlag(f, "version", c.Versions.CRIDockerd)
	case "containerd":
		setConfigFlag(f, "version", c.Versions.Containerd)
		setConfigFlag(f, "runc-version", c.Versions.Runc)
		setConfigFlag(f, "cni-version", c.Versions.CNIPlugins)
	}
	if !versionPattern.MatchString("v" + strings.TrimPrefix(*version, "v")) {
		return 2, errors.New("必须指定完整 --version vX.Y.Z 或配置 versions 对应项")
	}
	*version = "v" + strings.TrimPrefix(*version, "v")
	if *arch != "amd64" && *arch != "arm64" {
		return 2, errors.New("只支持 Linux amd64/arm64")
	}
	if *timeout <= 0 {
		return 2, errors.New("timeout 必须大于零")
	}
	base := c.Install.ReleaseBase
	if base == "" {
		base = defaultDownloadBase()
	}
	base = strings.TrimRight(base, "/")
	ver := strings.TrimPrefix(*version, "v")
	packages := []runtimePackage{}
	script := tool == "docker" && *method == "script"
	switch tool {
	case "docker":
		if *method != "script" && *method != "binary" {
			return 2, errors.New("method 必须为 script 或 binary")
		}
		if script {
			if *mirror != "Aliyun" && *mirror != "AzureChinaCloud" {
				return 2, errors.New("mirror 必须为 Aliyun 或 AzureChinaCloud")
			}
			if *source == "" {
				*source = c.Install.DockerScript
			}
			if *source == "" {
				*source = "https://get.docker.com"
			}
		} else {
			a := "x86_64"
			if *arch == "arm64" {
				a = "aarch64"
			}
			b := c.Install.DockerBinaryBase
			if b == "" {
				b = "https://mirrors.aliyun.com/docker-ce/linux/static/stable"
			}
			if *source == "" {
				*source = fmt.Sprintf("%s/%s/docker-%s.tgz", strings.TrimRight(b, "/"), a, ver)
			}
		}
	case "cri-dockerd":
		if *source == "" {
			*source = fmt.Sprintf("%s/Mirantis/cri-dockerd/releases/download/%s/cri-dockerd-%s.%s.tgz", base, *version, ver, *arch)
		}
	case "containerd":
		if !strings.HasPrefix(*version, "v1.") && !strings.HasPrefix(*version, "v2.") {
			return 2, errors.New("containerd 仅支持 1.x/2.x")
		}
		for _, v := range []string{*runc, *cni} {
			if !versionPattern.MatchString("v" + strings.TrimPrefix(v, "v")) {
				return 2, errors.New("runc/CNI 必须指定完整 vX.Y.Z 版本")
			}
		}
		if *source == "" {
			*source = fmt.Sprintf("%s/containerd/containerd/releases/download/%s/containerd-%s-linux-%s.tar.gz", base, *version, ver, *arch)
		}
		rv := "v" + strings.TrimPrefix(*runc, "v")
		cv := "v" + strings.TrimPrefix(*cni, "v")
		packages = append(packages, runtimePackage{name: "runc", version: rv, source: fmt.Sprintf("%s/opencontainers/runc/releases/download/%s/runc.%s", base, rv, *arch), raw: true}, runtimePackage{name: "cni", version: cv, source: fmt.Sprintf("%s/containernetworking/plugins/releases/download/%s/cni-plugins-linux-%s-%s.tgz", base, cv, *arch, cv)})
	}
	packages = append([]runtimePackage{{name: tool, version: *version, source: *source, checksum: *checksum}}, packages...)
	for _, p := range packages {
		u, e := url.Parse(p.source)
		if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
			return 2, errors.New("下载源必须是无用户凭据的 HTTP(S) URL")
		}
	}
	if *checksum != "" {
		b, e := hex.DecodeString(*checksum)
		if e != nil || len(b) != 32 {
			return 2, errors.New("sha256 必须为 64 位十六进制")
		}
	}
	fmt.Fprintln(out, "下载目录:", *downloadDir)
	fmt.Fprintf(out, "安装计划: %s %s，Linux/%s\n", tool, *version, *arch)
	if script {
		fmt.Fprintf(out, "方式: 官方脚本 --mirror %s --version %s\n", *mirror, ver)
	}
	for _, p := range packages {
		fmt.Fprintf(out, "下载: %s %s %s\n", p.name, p.version, p.source)
	}
	if script {
		fmt.Fprintln(out, "目标: 由官方脚本安装发行版软件包并管理 docker.service；拒绝已有运行时。")
	} else if tool == "containerd" {
		fmt.Fprintln(out, "目标: /usr/local/bin、/usr/local/sbin、/opt/cni/bin、/etc/containerd/config.toml、/etc/systemd/system；不覆盖已有文件。")
	} else {
		fmt.Fprintln(out, "目标: /usr/local/bin、/etc/systemd/system；不覆盖已有文件。")
	}
	fmt.Fprintln(out, "镜像源另执行 mirror 命令。")
	if *dryRun {
		fmt.Fprintln(out, "仅预览；去掉 --dry-run 执行安装。")
		return 0, nil
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != *arch {
		return 1, errors.New("必须在目标架构的 Linux 主机执行；macOS/Windows 请使用 Linux VM/WSL2")
	}
	if os.Geteuid() != 0 {
		return 1, errors.New("安装需要 root 权限")
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return 1, errors.New("安装需要正在运行的 systemd")
	}
	ctx, cancel := context.WithTimeout(parent, *timeout)
	defer cancel()
	if _, err := exec.LookPath("systemctl"); err != nil {
		return 1, err
	}
	// Fresh installs only: no overwrites of distribution or existing runtime installations.
	names := []string{tool}
	if tool == "docker" {
		names = []string{"docker", "dockerd", "containerd", "runc"}
	}
	if tool == "containerd" {
		names = []string{"containerd", "runc"}
	}
	for _, n := range names {
		if p, e := exec.LookPath(n); e == nil {
			return 1, fmt.Errorf("已存在 %s: %s；请保留现有安装，先人工处理版本迁移", n, p)
		}
	}
	unitName := tool
	if tool == "cri-dockerd" {
		unitName = "cri-docker"
		if err := commandRun(ctx, nil, out, diag, "docker", "info"); err != nil {
			return 1, fmt.Errorf("先安装并启动 Docker: %w", err)
		}
	}
	units := runtimeUnits(tool)
	for name := range units {
		if e := ensureRuntimeAbsent("/etc/systemd/system/" + name); e != nil {
			return 1, e
		}
		if b, e := commandOutput(ctx, nil, "systemctl", "--no-pager", "show", "-p", "LoadState", "--value", name); e != nil || strings.TrimSpace(string(b)) != "not-found" {
			return 1, fmt.Errorf("已有或无法检查 systemd unit %s，请先人工处理", name)
		}
	}
	if tool == "containerd" {
		if e := ensureRuntimeAbsent("/etc/containerd/config.toml"); e != nil {
			return 1, e
		}
	}
	if script {
		tmp, e := os.CreateTemp("", "opskit-docker-*.sh")
		if e != nil {
			return 1, e
		}
		defer os.Remove(tmp.Name())
		e = downloadRetained(ctx, *source, tmp, *downloadDir, tool, *version)
		closeErr := tmp.Close()
		if e != nil {
			return 1, e
		}
		if closeErr != nil {
			return 1, closeErr
		}
		if *checksum != "" {
			b, e := os.ReadFile(tmp.Name())
			if e != nil {
				return 1, e
			}
			if e = checkRuntimeHash(b, *checksum); e != nil {
				return 1, e
			}
		}
		if e = commandRun(ctx, nil, out, diag, "sh", tmp.Name(), "--mirror", *mirror, "--version", ver, "--channel", "stable"); e != nil {
			return 1, e
		}
	} else {
		// Download, extract and validate every package before touching installation paths.
		tmp, e := os.MkdirTemp("", "opskit-runtime-*")
		if e != nil {
			return 1, e
		}
		defer os.RemoveAll(tmp)
		files := map[string][]byte{}
		for i := range packages {
			p := &packages[i]
			var b runtimeDownloadBuffer
			if e = downloadRetained(ctx, p.source, &b, *downloadDir, p.name, p.version); e != nil {
				return 1, e
			}
			data := b.Bytes()
			if p.checksum != "" {
				if e = checkRuntimeHash(data, p.checksum); e != nil {
					return 1, e
				}
			}
			if p.raw {
				p.files = map[string][]byte{"runc": data}
			} else {
				p.files, e = runtimeArchive(data)
				if e != nil {
					return 1, e
				}
			}
			binary := p.name
			if binary == "docker" {
				binary = "dockerd"
			}
			if binary == "cni" {
				binary = "bridge"
			}
			content, ok := p.files[binary]
			if !ok {
				return 1, fmt.Errorf("%s 包缺少 %s", p.name, binary)
			}
			staged := filepath.Join(tmp, binary)
			if e = os.WriteFile(staged, content, 0700); e != nil {
				return 1, e
			}
			if p.name != "cni" {
				if e = verifyRuntimeVersion(ctx, staged, p.version); e != nil {
					return 1, e
				}
			}
			for n, content := range p.files {
				dest := "/usr/local/bin/" + n
				if p.name == "cni" {
					dest = "/opt/cni/bin/" + n
				}
				if p.name == "runc" {
					dest = "/usr/local/sbin/runc"
				}
				if _, exists := files[dest]; exists {
					return 1, fmt.Errorf("安装包文件重复: %s", dest)
				}
				files[dest] = content
			}
		}
		required := []string{}
		if tool == "docker" {
			required = []string{"docker", "dockerd", "containerd", "containerd-shim-runc-v2", "runc", "docker-proxy", "docker-init"}
		}
		if tool == "containerd" {
			required = []string{"containerd", "containerd-shim-runc-v2", "ctr"}
		}
		for _, n := range required {
			if _, ok := files["/usr/local/bin/"+n]; !ok {
				return 1, fmt.Errorf("安装包缺少必需文件 %s", n)
			}
		}
		configText := ""
		if tool == "containerd" {
			staged := filepath.Join(tmp, "containerd")
			b, e := commandOutput(ctx, nil, staged, "config", "default")
			if e != nil {
				return 1, e
			}
			configText, e = runtimeContainerdConfig(string(b))
			if e != nil {
				return 1, e
			}
			checkFile := filepath.Join(tmp, "config.toml")
			if e = os.WriteFile(checkFile, []byte(configText), 0600); e != nil {
				return 1, e
			}
			if _, e = commandOutput(ctx, nil, staged, "--config", checkFile, "config", "dump"); e != nil {
				return 1, e
			}
		}
		paths := make([]string, 0, len(files))
		for p := range files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			if e = ensureRuntimeAbsent(p); e != nil {
				return 1, e
			}
		}
		for _, p := range paths {
			if e = writeRuntimeNew(p, files[p], 0755); e != nil {
				return 1, e
			}
		}
		if tool == "containerd" {
			if e = writeRuntimeNew("/etc/containerd/config.toml", []byte(configText), 0600); e != nil {
				return 1, e
			}
		}
		for name, unit := range units {
			if e = writeRuntimeNew("/etc/systemd/system/"+name, []byte(unit), 0644); e != nil {
				return 1, e
			}
		}
	}
	if err = commandRun(ctx, nil, out, diag, "systemctl", "--no-pager", "daemon-reload"); err != nil {
		return 1, err
	}
	if err = commandRun(ctx, nil, out, diag, "systemctl", "--no-pager", "enable", "--now", unitName+".service"); err != nil {
		return 1, err
	}
	if err = commandRun(ctx, nil, out, diag, "systemctl", "--no-pager", "is-active", unitName+".service"); err != nil {
		return 1, err
	}
	binary := tool
	if binary == "docker" {
		binary = "dockerd"
	}
	if !script {
		binary = "/usr/local/bin/" + binary
	}
	if err = verifyRuntimeVersion(ctx, binary, *version); err != nil {
		return 1, err
	}
	fmt.Fprintf(out, "安装完成: %s %s；服务 %s 已启动\n", tool, *version, unitName)
	if tool == "cri-dockerd" {
		fmt.Fprintln(out, "kubeadm 使用 --cri-socket unix:///run/cri-dockerd.sock")
	}
	if tool == "containerd" {
		fmt.Fprintln(out, "Kubernetes 节点还需配置 overlay/br_netfilter、IP forwarding、CNI 网络与镜像源；安装器不修改主机网络参数。")
	}
	return 0, nil
}

func ensureRuntimeAbsent(p string) error {
	_, e := os.Lstat(p)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	return fmt.Errorf("目标已存在，不覆盖: %s", p)
}
func writeRuntimeNew(p string, b []byte, mode os.FileMode) error {
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		return e
	}
	f, e := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}
func checkRuntimeHash(b []byte, want string) error {
	s := sha256.Sum256(b)
	if !strings.EqualFold(hex.EncodeToString(s[:]), want) {
		return errors.New("安装包 SHA-256 不匹配")
	}
	return nil
}

var runtimeVersionRE = regexp.MustCompile(`\bv?([0-9]+\.[0-9]+\.[0-9]+)\b`)

func verifyRuntimeVersion(ctx context.Context, binary, version string) error {
	b, e := commandOutput(ctx, nil, binary, "--version")
	if e != nil {
		return e
	}
	m := runtimeVersionRE.FindStringSubmatch(string(b))
	if len(m) != 2 || m[1] != strings.TrimPrefix(version, "v") {
		return fmt.Errorf("%s 实际版本 %q 与目标 %s 不一致", binary, strings.TrimSpace(string(b)), version)
	}
	return nil
}

func runtimeArchive(data []byte) (map[string][]byte, error) {
	z, e := gzip.NewReader(bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	defer z.Close()
	t := tar.NewReader(z)
	files := map[string][]byte{}
	var total int64
	for {
		h, e := t.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		clean := path.Clean(h.Name)
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(h.Name, "\\") {
			return nil, fmt.Errorf("不安全的归档路径 %q", h.Name)
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return nil, fmt.Errorf("不支持的归档条目 %q", h.Name)
		}
		n := path.Base(clean)
		if n == "LICENSE" || n == "README.md" {
			continue
		}
		if h.Size < 0 || h.Size > 256<<20 || total+h.Size > 1<<30 {
			return nil, errors.New("安装包解压大小超限")
		}
		total += h.Size
		if _, ok := files[n]; ok {
			return nil, fmt.Errorf("归档同名文件 %s", n)
		}
		b, e := io.ReadAll(t)
		if e != nil {
			return nil, e
		}
		files[n] = b
	}
	return files, nil
}

func runtimeContainerdConfig(text string) (string, error) {
	// containerd 2.x emits single-quoted TOML section names; normalize section headers only.
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "[") {
			lines[i] = strings.ReplaceAll(l, "'", `"`)
		}
	}
	text = strings.Join(lines, "\n")
	var err error
	text, err = configureRegistry(text, true)
	if err != nil {
		return "", err
	}
	if !strings.Contains(text, "SystemdCgroup = false") && !strings.Contains(text, "SystemdCgroup = true") {
		return "", errors.New("containerd 默认配置缺少 SystemdCgroup")
	}
	text = strings.ReplaceAll(text, "SystemdCgroup = false", "SystemdCgroup = true")
	// Keep upstream sandbox image; node registry mirrors handle image transport.
	return text, nil
}

func runtimeUnits(tool string) map[string]string {
	binary := "/usr/local/bin/" + tool
	after := "network-online.target"
	extra := ""
	args := ""
	name := tool
	if tool == "docker" {
		binary = "/usr/local/bin/dockerd"
		extra = "Environment=PATH=/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin\n"
	}
	if tool == "containerd" {
		extra = "Environment=PATH=/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin\nExecStartPre=-/sbin/modprobe overlay\n"
	}
	if tool == "cri-dockerd" {
		name = "cri-docker"
		after += " docker.service"
		args = " --container-runtime-endpoint unix:///run/cri-dockerd.sock"
	}
	unit := fmt.Sprintf("[Unit]\nDescription=opskit %s\nAfter=%s\nWants=network-online.target\n\n[Service]\nType=notify\n%sExecStart=%s%s\nDelegate=yes\nKillMode=process\nRestart=on-failure\nRestartSec=5\nLimitNOFILE=infinity\nTasksMax=infinity\n\n[Install]\nWantedBy=multi-user.target\n", tool, after, extra, binary, args)
	return map[string]string{name + ".service": unit}
}

// Bound compressed downloads as well as expanded tar entries.
type runtimeDownloadBuffer struct{ bytes.Buffer }

func (b *runtimeDownloadBuffer) Write(p []byte) (int, error) {
	if len(p) > (256<<20)-b.Len() {
		return 0, errors.New("安装包下载大小超过 256 MiB")
	}
	return b.Buffer.Write(p)
}
