package app

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type registryMirror struct {
	Server       string `yaml:"server"`
	Endpoint     string `yaml:"endpoint"`
	SkipVerify   bool   `yaml:"skip_verify"`
	OverridePath bool   `yaml:"override_path"`
}
type mirrorSettings struct {
	Docker     []string                  `yaml:"docker"`
	Containerd map[string]registryMirror `yaml:"containerd"`
	Go         string                    `yaml:"go"`
	NPM        string                    `yaml:"npm"`
	Pip        string                    `yaml:"pip"`
	Maven      string                    `yaml:"maven"`
}

func validURL(s string) bool {
	u, e := url.Parse(s)
	return e == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil
}
func renderHosts(registries map[string]registryMirror) (map[string][]byte, error) {
	if len(registries) == 0 {
		return nil, errors.New("请先填写 mirrors.containerd，不猜测代理地址")
	}
	files := map[string][]byte{}
	for registry, m := range registries {
		if registry == "." || registry == ".." || strings.ContainsAny(registry, "/\\\r\n") || registry == "" {
			return nil, fmt.Errorf("非法 registry: %q", registry)
		}
		if !validURL(m.Server) || (m.Endpoint != "" && !validURL(m.Endpoint)) {
			return nil, fmt.Errorf("registry %s 的 server/endpoint 必须为 HTTP(S) 地址且不能包含凭据", registry)
		}
		text := "server = " + strconv.Quote(m.Server) + "\n"
		if m.Endpoint != "" {
			text += fmt.Sprintf("\n[host.%s]\n  capabilities = [\"pull\", \"resolve\"]\n  skip_verify = %t\n  override_path = %t\n", strconv.Quote(m.Endpoint), m.SkipVerify, m.OverridePath)
		}
		files[registry] = []byte(text)
	}
	return files, nil
}
func configureRegistry(text string, allowLegacy bool) (string, error) {
	// Recognize containerd 1.x config v2 and containerd 2.x config v3. Unknown layouts fail closed.
	section := ""
	for _, candidate := range []string{`[plugins."io.containerd.grpc.v1.cri".registry]`, `[plugins."io.containerd.cri.v1.images".registry]`} {
		if strings.Contains(text, candidate) {
			section = candidate
			break
		}
	}
	if section == "" {
		return "", errors.New("未识别的 containerd registry 配置；请先使用受支持的 containerd 主配置")
	}
	prefix := strings.TrimSuffix(section, "]")
	var lines []string
	inRegistry, skip, authSection := false, false, false
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") {
			inRegistry = trim == section
			legacy := strings.HasPrefix(trim, prefix+".mirrors")
			authSection = strings.HasPrefix(trim, prefix+".configs") || strings.HasPrefix(trim, prefix+".auths")
			if legacy && !allowLegacy {
				return "", errors.New("主机存在旧 registry mirrors/configs/auths，先人工迁移认证配置；未修改文件")
			}
			skip = legacy
		}
		if authSection && trim != "" && !strings.HasPrefix(trim, "[") && !strings.HasPrefix(trim, "#") {
			return "", errors.New("存在旧 registry 认证/TLS 配置，必须先人工迁移，未写入文件")
		}
		if skip {
			continue
		}
		if inRegistry && strings.HasPrefix(trim, "config_path") {
			continue
		}
		lines = append(lines, line)
		if trim == section {
			lines = append(lines, `  config_path = "/etc/containerd/certs.d"`)
		}
	}
	return strings.Join(lines, "\n"), nil
}
func writeConfigFile(path string, data []byte, out io.Writer) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	mode := os.FileMode(0600)
	var original []byte
	existed := false
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("拒绝覆盖非普通文件: %s", path)
		}
		mode = info.Mode().Perm()
		existed = true
		old, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		original = old
		if string(old) == string(data) {
			return nil
		}
		backup, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".backup-*")
		if err != nil {
			return err
		}
		if _, err = backup.Write(old); err != nil {
			backup.Close()
			return err
		}
		if err = backup.Close(); err != nil {
			return err
		}
		fmt.Fprintln(out, "备份:", backup.Name())
	} else if !os.IsNotExist(err) {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".opskit-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	// On Windows replace the destination only after a backup has been created.
	if runtime.GOOS == "windows" {
		if _, e := os.Stat(path); e == nil {
			if err = os.Remove(path); err != nil {
				return err
			}
		}
	}
	err = os.Rename(temp.Name(), path)
	if err != nil && runtime.GOOS == "windows" && existed {
		if restoreErr := os.WriteFile(path, original, mode); restoreErr != nil {
			return fmt.Errorf("写入失败: %v；恢复失败: %v；请从备份恢复", err, restoreErr)
		}
	}
	return err
}
func readOptional(path string) ([]byte, error) {
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return nil, nil
	}
	return b, e
}
func replaceSetting(text, section, key, value string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	current := ""
	placed := false
	var result []string
	insert := func() {
		if !placed {
			result = append(result, key+" = "+value)
			placed = true
		}
	}
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			if current == section {
				insert()
			}
			current = strings.Trim(trim, "[]")
		}
		if current == section && strings.HasPrefix(trim, key) {
			parts := strings.SplitN(trim, "=", 2)
			if len(parts) == 2 && strings.TrimSpace(parts[0]) == key {
				if !placed {
					result = append(result, key+" = "+value)
					placed = true
				}
				continue
			}
		}
		result = append(result, line)
	}
	if !placed {
		if section != "" && current != section {
			result = append(result, "["+section+"]")
		}
		insert()
	}
	return strings.Join(result, "\n") + "\n"
}
func renderLanguage(tool string, old []byte, source string) ([]byte, error) {
	if strings.ContainsAny(source, "\r\n") || !validURL(source) {
		return nil, errors.New("镜像源须为有效 HTTP(S) 地址，不得包含换行或凭据")
	}
	switch tool {
	case "go":
		return []byte(strings.ReplaceAll(replaceSetting(string(old), "", "GOPROXY", source), "GOPROXY = ", "GOPROXY=")), nil
	case "npm":
		return []byte(replaceSetting(string(old), "", "registry", source)), nil
	case "pip":
		return []byte(replaceSetting(string(old), "global", "index-url", source)), nil
	case "maven":
		var escaped strings.Builder
		_ = xml.EscapeText(&escaped, []byte(source))
		mirror := `<mirrors><mirror><id>opskit</id><mirrorOf>*</mirrorOf><url>` + escaped.String() + `</url></mirror></mirrors>`
		if len(old) == 0 {
			return []byte("<settings>" + mirror + "</settings>\n"), nil
		}
		decoder := xml.NewDecoder(strings.NewReader(string(old)))
		for {
			_, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
		}
		if start := strings.Index(string(old), "<mirrors><mirror><id>opskit</id>"); start >= 0 {
			end := strings.Index(string(old)[start:], "</mirrors>")
			if end < 0 {
				return nil, errors.New("opskit mirrors 未闭合")
			}
			block := string(old)[start : start+end+len("</mirrors>")]
			if strings.Count(block, "<mirror>") != 1 {
				return nil, errors.New("存在额外 Maven mirror，请人工合并")
			}
			return []byte(strings.Replace(string(old), block, mirror, 1)), nil
		}
		if strings.Contains(string(old), "<mirrors") {
			return nil, errors.New("现有 Maven 已包含 mirrors，保留原配置；请显式指定新 --file 或人工合并")
		}
		if !strings.Contains(string(old), "</settings>") {
			return nil, errors.New("settings.xml 缺少 settings 根节点")
		}
		return []byte(strings.Replace(string(old), "</settings>", mirror+"</settings>", 1)), nil
	}
	return nil, errors.New("未知语言镜像源")
}
func runMirror(parent context.Context, args []string, out, diag io.Writer) (int, error) {
	if len(args) == 0 {
		return 2, errors.New("mirror docker|containerd|go|npm|pip|maven")
	}
	tool := args[0]
	switch tool {
	case "docker", "containerd", "go", "npm", "pip", "maven":
	default:
		return 2, fmt.Errorf("未知 mirror 类型: %s", tool)
	}
	f := flag.NewFlagSet("mirror "+tool, flag.ContinueOnError)
	f.SetOutput(diag)
	configPath := f.String("config", defaultConfigPath, "统一配置文件")
	apply := f.Bool("apply", false, "写入配置；默认预览")
	file := f.String("file", "", "覆盖配置文件路径")
	source := f.String("url", "", "语言工具镜像地址")
	kindName := f.String("kind", "", "修改指定 kind 的全部 containerd 节点；不修改宿主机")
	kindBin := f.String("kind-bin", "kind", "kind 可执行文件")
	timeout := f.Duration("timeout", 15*time.Minute, "任务超时")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	if f.NArg() != 0 || *timeout <= 0 {
		return 2, errors.New("参数或 timeout 无效")
	}
	c, err := commandConfig(f, *configPath)
	if err != nil {
		return 2, err
	}
	setEnvFlag(f, "kind-bin", "OPSKIT_KIND_BIN")
	setConfigFlag(f, "kind-bin", c.Kind.Binary)
	*kindBin = configuredKindBinary(c, *kindBin)
	ctx, cancel := context.WithTimeout(parent, *timeout)
	defer cancel()
	if *kindName != "" {
		if tool != "containerd" || *file != "" {
			return 2, errors.New("--kind 仅与 mirror containerd 一起使用，不能指定 --file")
		}
		err = mirrorKindNodes(ctx, *kindName, *kindBin, c.Mirrors.Containerd, *apply, out, diag)
		if err != nil {
			return 1, err
		}
		return 0, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return 1, err
	}
	if *file == "" {
		switch tool {
		case "docker":
			if runtime.GOOS != "linux" {
				return 2, errors.New("Docker Desktop 请显式 --file 指定配置导出文件；本机 daemon.json 自动定位仅支持 Linux")
			}
			*file = "/etc/docker/daemon.json"
		case "containerd":
			if runtime.GOOS != "linux" {
				return 2, errors.New("宿主机 containerd 仅支持 Linux；其他平台可使用 --kind")
			}
			*file = "/etc/containerd/config.toml"
		case "go":
			b, e := commandOutput(ctx, nil, "go", "env", "GOENV")
			if e != nil {
				return 3, e
			}
			*file = strings.TrimSpace(string(b))
			if *file == "off" || *file == "" {
				return 2, errors.New("Go GOENV 已关闭，请显式 --file")
			}
		case "npm":
			*file = filepath.Join(home, ".npmrc")
		case "pip":
			if runtime.GOOS == "windows" {
				*file = filepath.Join(os.Getenv("APPDATA"), "pip", "pip.ini")
			} else {
				*file = filepath.Join(home, ".config", "pip", "pip.conf")
			}
		case "maven":
			*file = filepath.Join(home, ".m2", "settings.xml")
		}
	}
	old, err := readOptional(*file)
	if err != nil {
		return 1, err
	}
	var data, preview []byte
	switch tool {
	case "docker":
		if len(c.Mirrors.Docker) == 0 {
			return 2, errors.New("请填写 mirrors.docker")
		}
		for _, u := range c.Mirrors.Docker {
			if !validURL(u) {
				return 2, errors.New("无效 Docker mirror URL")
			}
		}
		values := map[string]any{}
		if len(old) > 0 {
			if err = json.Unmarshal(old, &values); err != nil {
				return 2, err
			}
		}
		if values == nil {
			return 2, errors.New("daemon.json 必须为 JSON 对象，不能是 null")
		}
		values["registry-mirrors"] = c.Mirrors.Docker
		data, err = json.MarshalIndent(values, "", "  ")
		preview, _ = json.MarshalIndent(map[string]any{"registry-mirrors": c.Mirrors.Docker}, "", "  ")
	case "containerd":
		files, e := renderHosts(c.Mirrors.Containerd)
		if e != nil {
			return 2, e
		}
		if len(old) == 0 {
			return 2, errors.New("containerd 主配置不存在，先安装 containerd")
		}
		updated, e := configureRegistry(string(old), false)
		if e != nil {
			return 2, e
		}
		data = []byte(updated)
		fmt.Fprintf(out, "containerd: %s → config_path=/etc/containerd/certs.d；将写入 %d 份 hosts.toml\n", *file, len(files))
		if !*apply {
			return 0, nil
		}
		if filepath.Clean(*file) != "/etc/containerd/config.toml" {
			return 2, errors.New("宿主机 apply 仅允许标准 config.toml 路径，以保证 certs.d 对应实际运行时")
		}
		// Validate with the installed runtime before touching its live configuration.
		tmp, e := os.CreateTemp("", "opskit-containerd-*.toml")
		if e != nil {
			return 1, e
		}
		defer os.Remove(tmp.Name())
		if _, e = tmp.Write(data); e != nil {
			tmp.Close()
			return 1, e
		}
		if e = tmp.Close(); e != nil {
			return 1, e
		}
		if _, e = commandOutput(ctx, nil, "containerd", "--config", tmp.Name(), "config", "dump"); e != nil {
			return 1, e
		}
		for registry, content := range files {
			if e = writeConfigFile(filepath.Join("/etc/containerd/certs.d", registry, "hosts.toml"), content, out); e != nil {
				return 1, e
			}
		}
	default:
		if *source == "" {
			switch tool {
			case "go":
				*source = c.Mirrors.Go
			case "npm":
				*source = c.Mirrors.NPM
			case "pip":
				*source = c.Mirrors.Pip
			case "maven":
				*source = c.Mirrors.Maven
			}
		}
		data, err = renderLanguage(tool, old, *source)
		if err == nil {
			preview, err = renderLanguage(tool, nil, *source)
		}
	}
	if err != nil {
		return 2, err
	}
	fmt.Fprintf(out, "mirror %s: %s\n", tool, *file)
	if !*apply {
		_, err = out.Write(preview)
		fmt.Fprintln(out, "\n预览完成；添加 --apply 写入并备份。")
		return 0, err
	}
	if err = writeConfigFile(*file, data, out); err != nil {
		return 1, err
	}
	if tool == "docker" || tool == "containerd" {
		fmt.Fprintf(out, "配置已写入；请在维护窗口重启 %s 服务。\n", tool)
	}
	return 0, nil
}
func mirrorKindNodes(ctx context.Context, name, binary string, registries map[string]registryMirror, apply bool, out, diag io.Writer) error {
	if !clusterNamePattern.MatchString(name) {
		return errors.New("非法 kind 集群名")
	}
	files, err := renderHosts(registries)
	if err != nil {
		return err
	}
	nodes, err := commandOutput(ctx, nil, binary, "get", "nodes", "--name", name)
	if err != nil {
		return err
	}
	names := strings.Fields(string(nodes))
	if len(names) == 0 {
		return errors.New("未找到 kind 节点")
	}
	// Read and validate all node configs before modifying any node.
	configs := map[string]string{}
	for _, node := range names {
		data, e := commandOutput(ctx, nil, "docker", "exec", node, "cat", "/etc/containerd/config.toml")
		if e != nil {
			return e
		}
		text, e := configureRegistry(string(data), true)
		if e != nil {
			return fmt.Errorf("%s: %w", node, e)
		}
		configs[node] = text
	}
	fmt.Fprintf(out, "kind %s: %d 个节点，%d 个 registry\n", name, len(names), len(files))
	if !apply {
		return nil
	}
	dir, err := os.MkdirTemp("", "opskit-mirrors-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	registriesOrder := make([]string, 0, len(files))
	for registry := range files {
		registriesOrder = append(registriesOrder, registry)
	}
	sort.Strings(registriesOrder)
	for _, node := range names {
		raw, e := commandOutput(ctx, nil, "docker", "exec", node, "mktemp", "-d", "/etc/containerd/opskit-backup.XXXXXXXX")
		if e != nil {
			return e
		}
		backup := strings.TrimSpace(string(raw))
		if !strings.HasPrefix(backup, "/etc/containerd/opskit-backup.") {
			return errors.New("节点备份路径异常")
		}
		if _, e = commandOutput(ctx, nil, "docker", "exec", node, "cp", "-a", "/etc/containerd/config.toml", backup+"/config.toml"); e != nil {
			return e
		}
		if _, e = commandOutput(ctx, nil, "docker", "exec", node, "test", "-d", "/etc/containerd/certs.d"); e == nil {
			if _, e = commandOutput(ctx, nil, "docker", "exec", node, "cp", "-a", "/etc/containerd/certs.d", backup+"/certs.d"); e != nil {
				return e
			}
		}
		local := filepath.Join(dir, "config.toml")
		if e = os.WriteFile(local, []byte(configs[node]), 0600); e != nil {
			return e
		}
		if _, e = commandOutput(ctx, nil, "docker", "cp", local, node+":"+backup+"/config.new.toml"); e != nil {
			return e
		}
		if _, e = commandOutput(ctx, nil, "docker", "exec", node, "containerd", "--config", backup+"/config.new.toml", "config", "dump"); e != nil {
			return e
		}
		for i, registry := range registriesOrder {
			local = filepath.Join(dir, strconv.Itoa(i)+".toml")
			if e = os.WriteFile(local, files[registry], 0600); e != nil {
				return e
			}
			remote := "/etc/containerd/certs.d/" + registry
			if _, e = commandOutput(ctx, nil, "docker", "exec", node, "mkdir", "-p", remote); e != nil {
				return e
			}
			if _, e = commandOutput(ctx, nil, "docker", "cp", local, node+":"+remote+"/hosts.toml"); e != nil {
				return e
			}
		}
		if _, e = commandOutput(ctx, nil, "docker", "exec", node, "cp", backup+"/config.new.toml", "/etc/containerd/config.toml"); e != nil {
			return e
		}
		if e = commandRun(ctx, nil, out, diag, "docker", "exec", node, "systemctl", "--no-pager", "restart", "containerd"); e != nil {
			return e
		}
		if _, e = commandOutput(ctx, nil, "docker", "exec", node, "crictl", "--runtime-endpoint", "unix:///run/containerd/containerd.sock", "info"); e != nil {
			return e
		}
		if _, ok := registries["docker.io"]; ok {
			if e = commandRun(ctx, nil, out, diag, "docker", "exec", node, "crictl", "--runtime-endpoint", "unix:///run/containerd/containerd.sock", "--image-endpoint", "unix:///run/containerd/containerd.sock", "pull", "docker.io/library/busybox:1.36"); e != nil {
				return e
			}
		}
		fmt.Fprintf(out, "已更新 %s；备份: %s\n", node, backup)
	}
	return nil
}
