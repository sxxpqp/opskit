package app

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func configureDockerInotify(ctx context.Context, file string, out, diag io.Writer) error {
	old, err := readOptional(file)
	if err != nil {
		return err
	}
	settings := []struct {
		key   string
		value uint64
	}{
		{"fs.inotify.max_user_instances", 8192},
		{"fs.inotify.max_user_watches", 524288},
	}
	current := func(key string) (uint64, error) {
		raw, err := commandOutput(ctx, nil, "sysctl", "-n", key)
		if err != nil {
			return 0, err
		}
		value, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("读取 %s 数值失败: %w", key, err)
		}
		return value, nil
	}
	text := string(old)
	for i := range settings {
		setting := &settings[i]
		running, err := current(setting.key)
		if err != nil {
			return err
		}
		if running > setting.value {
			setting.value = running
		}
		// Preserve higher persistent values even when they have not been applied yet.
		for _, line := range strings.Split(string(old), "\n") {
			line, _, _ = strings.Cut(line, "#")
			key, value, ok := strings.Cut(line, "=")
			if !ok || strings.TrimSpace(key) != setting.key {
				continue
			}
			saved, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			if err != nil {
				return fmt.Errorf("%s 中 %s 数值无效: %w", file, setting.key, err)
			}
			if saved > setting.value {
				setting.value = saved
			}
		}
		text = replaceSetting(text, "", setting.key, strconv.FormatUint(setting.value, 10))
	}
	if err = writeConfigFile(file, []byte(text), out); err != nil {
		return err
	}
	if err = commandRun(ctx, nil, out, diag, "sysctl", "-p", file); err != nil {
		return fmt.Errorf("inotify 配置已保存到 %s，但应用失败: %w", file, err)
	}
	for _, setting := range settings {
		actual, err := current(setting.key)
		if err != nil {
			return err
		}
		if actual < setting.value {
			return fmt.Errorf("%s 未生效：要求至少 %d，实际 %d", setting.key, setting.value, actual)
		}
		fmt.Fprintf(out, "%s = %d（已生效）\n", setting.key, actual)
	}
	return nil
}
