package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

type kindOptions struct{ version, binDir, base, platform, arch, downloadDir string }

func kindAsset(platform, arch string) (string, string, error) {
	if (platform != "linux" && platform != "darwin" && platform != "windows") ||
		(arch != "amd64" && arch != "arm64") || (platform == "windows" && arch != "amd64") {
		return "", "", fmt.Errorf("不支持的 kind 发布平台: %s/%s", platform, arch)
	}
	binary := "kind"
	if platform == "windows" {
		binary += ".exe"
	}
	return "kind-" + platform + "-" + arch, binary, nil
}

func verifyKind(ctx context.Context, path, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("运行 kind version 失败: %w: %s", err, output)
	}
	fields := strings.Fields(string(output))
	if len(fields) < 2 || fields[0] != "kind" || fields[1] != version {
		return fmt.Errorf("kind 版本不匹配，要求 %s，返回 %s", version, strings.TrimSpace(string(output)))
	}
	return nil
}

func download(ctx context.Context, source string, destination io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	for attempt := 1; attempt <= 3; attempt++ {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusOK {
			_, err = io.Copy(destination, resp.Body)
			resp.Body.Close()
			return err
		}
		status := resp.StatusCode
		resp.Body.Close()
		retryable := status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
		if !retryable || attempt == 3 {
			return fmt.Errorf("下载 %s: HTTP %d（尝试 %d 次）", source, status, attempt)
		}
		// Retry before copying any bytes: never append a second response to a
		// partial binary or a checksum stream. Respect the caller's total timeout.
		timer := time.NewTimer(time.Duration(attempt*2) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func installKind(ctx context.Context, o kindOptions, out io.Writer) error {
	asset, binary, err := kindAsset(o.platform, o.arch)
	if err != nil {
		return err
	}
	source := strings.TrimRight(o.base, "/") + "/kubernetes-sigs/kind/releases/download/" + o.version + "/" + asset
	return installVerifiedBinary(ctx, binaryInstallOptions{kindOptions: o, tool: "kind", binary: binary, source: source, checksumSuffix: ".sha256sum", verify: verifyKind}, out)
}
