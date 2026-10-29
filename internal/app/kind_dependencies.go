package app

import (
	"fmt"
	"io"
	"os/exec"
)

func kindKubectlInstallHint(version string) string {
	command := "opskit kubectl install"
	if versionPattern.MatchString(version) {
		command += " --version " + version
	}
	return command
}

func kindDockerInstallHint(platform string) string {
	if platform == "linux" {
		return "安装 Docker：sudo opskit docker install；已有 Docker 时请启动服务并确认当前用户可运行 docker info"
	}
	return "请安装并启动 Docker Desktop（Linux 容器），或连接可用的 Docker Engine，然后运行 docker info 检查"
}

func printKindDependencyHints(out io.Writer, platform, version string) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		fmt.Fprintf(out, "提示：未找到 kubectl；创建和管理 kind 集群前请执行：%s\n", kindKubectlInstallHint(version))
	}
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Fprintf(out, "提示：未找到 Docker；%s\n", kindDockerInstallHint(platform))
	}
}
