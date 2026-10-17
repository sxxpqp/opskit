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
}`

func finishKindPath(dir string, out io.Writer) error {
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
			return fmt.Errorf("kind 命令入口已创建；持久化 PATH 需要 PowerShell: %w", e)
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
		fmt.Fprintf(out, "当前 shell %s 未自动配置；请将 %s 加入 PATH。\n", shell, dir)
		return nil
	}
	for _, profile := range profiles {
		text, e := readOptional(profile)
		if e != nil {
			return e
		}
		if !strings.Contains(string(text), line) {
			data := append(append([]byte{}, text...), []byte("\n# opskit kind PATH\n"+line+"\n")...)
			if e = writeConfigFile(profile, data, out); e != nil {
				return fmt.Errorf("kind 入口已创建，但写入 PATH 配置失败: %w", e)
			}
		}
		fmt.Fprintln(out, "PATH 配置:", profile)
	}
	fmt.Fprintf(out, "重新打开终端后生效；当前终端执行: %s\n", line)
	return nil
}
