// Package k8s contains Kubernetes client tool metadata and version verification.
package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func KubectlAsset(platform, arch string) (string, error) {
	if (platform != "linux" && platform != "darwin" && platform != "windows") || (arch != "amd64" && arch != "arm64") || (platform == "windows" && arch != "amd64") {
		return "", fmt.Errorf("不支持的 kubectl 发布平台: %s/%s（Windows 当前支持 amd64）", platform, arch)
	}
	if platform == "windows" {
		return "kubectl.exe", nil
	}
	return "kubectl", nil
}

func VerifyKubectl(parent context.Context, binary, version string) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "version", "--client=true", "--output=json").CombinedOutput()
	if err != nil {
		return fmt.Errorf("运行 kubectl 客户端版本检查失败: %w: %s", err, output)
	}
	var result struct {
		ClientVersion struct {
			GitVersion string `json:"gitVersion"`
		} `json:"clientVersion"`
	}
	if err = json.Unmarshal(output, &result); err != nil {
		return fmt.Errorf("kubectl 客户端版本 JSON 无效: %w", err)
	}
	if result.ClientVersion.GitVersion != version {
		return fmt.Errorf("kubectl 版本不匹配，要求 %s，返回 %s", version, strings.TrimSpace(result.ClientVersion.GitVersion))
	}
	return nil
}
