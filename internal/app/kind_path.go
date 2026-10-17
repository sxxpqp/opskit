package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func kindLinkCandidates(platform, home, localAppData string, root bool) []string {
	if platform != "windows" && root {
		return []string{"/usr/local/bin"}
	}
	if platform == "windows" {
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		return []string{filepath.Join(localAppData, "opskit", "bin")}
	}
	userBin := filepath.Join(home, ".local", "bin")
	if platform == "darwin" {
		return []string{"/opt/homebrew/bin", "/usr/local/bin", userBin}
	}
	return []string{userBin}
}

func pathIncludes(list, dir string) bool {
	for _, p := range filepath.SplitList(list) {
		if p == "" {
			continue
		}
		if runtime.GOOS == "windows" {
			if strings.EqualFold(filepath.Clean(p), filepath.Clean(dir)) {
				return true
			}
		} else if filepath.Clean(p) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

func shellPathLine(dir string) string {
	return "export PATH='" + strings.ReplaceAll(dir, "'", "'\"'\"'") + "':\"$PATH\""
}

func kindPathProfiles(home, shell, zdotdir string) []string {
	switch strings.TrimSuffix(filepath.Base(shell), ".exe") {
	case "zsh":
		if zdotdir != "" {
			return []string{filepath.Join(zdotdir, ".zshrc")}
		}
		return []string{filepath.Join(home, ".zshrc")}
	case "bash":
		login := filepath.Join(home, ".profile")
		for _, name := range []string{".bash_profile", ".bash_login"} {
			p := filepath.Join(home, name)
			if _, e := os.Stat(p); e == nil {
				login = p
				break
			}
		}
		return []string{filepath.Join(home, ".bashrc"), login}
	case "sh", "dash", "ksh":
		return []string{filepath.Join(home, ".profile")}
	}
	return nil
}

const windowsKindPathScript = `$ErrorActionPreference = 'Stop'
$dir = $env:OPSKIT_KIND_LINK_DIR
$old = [Environment]::GetEnvironmentVariable('Path', 'User')
$found = $false
foreach ($part in ($old -split ';')) {
 if ([string]::Equals($part.TrimEnd('\'), $dir.TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase)) { $found = $true }
}
if (-not $found) {
 $new = $dir
 if (-not [string]::IsNullOrEmpty($old)) { $new = $old.TrimEnd(';') + ';' + $dir }
 [Environment]::SetEnvironmentVariable('Path', $new, 'User')
}
$saved = [Environment]::GetEnvironmentVariable('Path', 'User')
$verified = $false
foreach ($part in ($saved -split ';')) {
 if ([string]::Equals($part.TrimEnd('\'), $dir.TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase)) { $verified = $true }
}
if (-not $verified) { throw 'User PATH read-back verification failed' }`

func finishKindPath(dir string, out io.Writer) error { return finishToolPath(dir, "kind", out) }
func finishToolPath(dir, tool string, out io.Writer) error {
	if pathIncludes(os.Getenv("PATH"), dir) {
		return nil
	}
	if runtime.GOOS == "windows" {
		// Update user scope, preserving existing entries; never use setx (which can truncate PATH).
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		executable, e := exec.LookPath("powershell.exe")
		if e != nil {
			executable, e = exec.LookPath("pwsh.exe")
		}
		if e != nil {
			return fmt.Errorf("%s 命令入口已创建；持久化 PATH 需要 PowerShell: %w", tool, e)
		}
		cmd := exec.CommandContext(ctx, executable, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", windowsKindPathScript)
		cmd.Env = append(os.Environ(), "OPSKIT_KIND_LINK_DIR="+dir)
		if b, e := cmd.CombinedOutput(); e != nil {
			return fmt.Errorf("保存用户 PATH 失败: %w: %s", e, b)
		}
		fmt.Fprintln(out, "已加入 Windows 用户 PATH；重新打开终端后生效。")
		fmt.Fprintf(out, "当前 PowerShell 可执行: $env:Path = '%s;' + $env:Path\n", strings.ReplaceAll(dir, "'", "''"))
		return nil
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		if runtime.GOOS == "darwin" {
			shell = "/bin/zsh"
		} else {
			shell = "/bin/sh"
		}
	}
	line := shellPathLine(dir)
	profiles := kindPathProfiles(home, shell, os.Getenv("ZDOTDIR"))
	if len(profiles) == 0 {
		return fmt.Errorf("%s 入口已创建，shell %s 不支持自动 PATH 配置；请将 %s 加入 PATH", tool, shell, dir)
	}
	for _, profile := range profiles {
		text, e := readOptional(profile)
		if e != nil {
			return e
		}
		if !strings.Contains(string(text), line) {
			data := append(append([]byte{}, text...), []byte("\n# opskit "+tool+" PATH\n"+line+"\n")...)
			if e = writeConfigFile(profile, data, out); e != nil {
				return fmt.Errorf("%s 入口已创建，但写入 PATH 配置失败: %w", tool, e)
			}
		}
		fmt.Fprintln(out, "PATH 配置:", profile)
	}
	fmt.Fprintf(out, "重新打开终端后生效；当前终端执行: %s\n", line)
	return nil
}

// Verify the installed entry separately from the command that the current PATH resolves.
func verifyKindCommand(ctx context.Context, dir, version string, out io.Writer) error {
	_, binary, err := kindAsset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	return verifyToolCommand(ctx, dir, "kind", binary, version, verifyKind, out)
}
func verifyToolCommand(ctx context.Context, dir, tool, binary, version string, verify func(context.Context, string, string) error, out io.Writer) error {
	var err error
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	entry, err := filepath.Abs(filepath.Join(dir, binary))
	if err != nil {
		return err
	}
	if err = verify(ctx, entry, version); err != nil {
		return fmt.Errorf("%s 命令入口验证失败: %w", tool, err)
	}
	resolved, lookupErr := exec.LookPath(binary)
	if !pathIncludes(os.Getenv("PATH"), dir) {
		fmt.Fprintf(out, "入口版本验证通过: %s (%s)\n", entry, version)
		if lookupErr == nil {
			fmt.Fprintln(out, "当前 PATH 仍命中:", resolved)
		}
		fmt.Fprintf(out, "PATH 待生效：请按以上提示更新当前终端，或重开终端后验证 %s 客户端版本。\n", tool)
		return nil
	}
	if lookupErr != nil {
		return fmt.Errorf("%s 入口存在，但当前 PATH 无法找到命令: %w", tool, lookupErr)
	}
	actual, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return err
	}
	expected, err := filepath.EvalSymlinks(entry)
	if err != nil {
		return err
	}
	actual, err = filepath.Abs(actual)
	if err != nil {
		return err
	}
	expected, err = filepath.Abs(expected)
	if err != nil {
		return err
	}
	matches := filepath.Clean(actual) == filepath.Clean(expected)
	if runtime.GOOS == "windows" {
		matches = strings.EqualFold(filepath.Clean(actual), filepath.Clean(expected))
	}
	if !matches {
		return fmt.Errorf("PATH 优先命中其他 %s: %s；本次入口为 %s，请调整 PATH 顺序后重试", tool, resolved, entry)
	}
	if err = verify(ctx, resolved, version); err != nil {
		return fmt.Errorf("PATH 中的 %s 版本验证失败: %w", tool, err)
	}
	fmt.Fprintf(out, "PATH 验证通过: %s (%s)\n", resolved, version)
	return nil
}
