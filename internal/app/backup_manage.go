package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"
)

// Only complete, flat, owned task directories are eligible for pruning.
func verifiedBackupTask(directory string) (Manifest, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Manifest{}, errors.New("备份任务必须是普通目录")
	}
	raw, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return manifest, err
	}
	if manifest.SchemaVersion != 1 || manifest.Created.IsZero() || manifest.Size <= 0 || !validSHA256(manifest.SHA256) || (manifest.File != "snapshot.db" && manifest.File != "backup.sql" && manifest.File != "backup.sql.gz") {
		return manifest, errors.New("无效的备份清单")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return manifest, err
	}
	if len(entries) != 2 {
		return manifest, errors.New("目录包含额外文件，拒绝自动处理")
	}
	for _, entry := range entries {
		if entry.Name() != "manifest.json" && entry.Name() != manifest.File {
			return manifest, errors.New("目录包含未知文件")
		}
		info, err := os.Lstat(filepath.Join(directory, entry.Name()))
		if err != nil || !info.Mode().IsRegular() {
			return manifest, errors.New("拒绝链接或特殊文件")
		}
	}
	actual, err := inspect(filepath.Join(directory, manifest.File))
	if err != nil {
		return manifest, err
	}
	if actual.SHA256 != manifest.SHA256 || actual.Size != manifest.Size {
		return manifest, errors.New("备份内容与清单不匹配")
	}
	return manifest, nil
}

func newBackupManageCommand(parent context.Context, action string, out io.Writer) (*cobra.Command, error) {
	cmd := &cobra.Command{Use: action, Short: "校验本地备份或预览保留策略清理"}
	f := cmd.Flags()
	config := f.String("config", defaultConfigPath, "统一 YAML 配置；读取 backup.retention")
	directory := f.String("directory", "", "verify: 单个任务目录；prune: 备份根目录")
	keep := f.Int("keep", 7, "至少保留最新的完整备份份数，最小 1")
	age := f.Duration("older-than", 30*24*time.Hour, "仅清理早于该时长且超出 keep 的完整备份")
	dry := f.Bool("dry-run", true, "只列出清理计划；--dry-run=false 执行本地清理")
	bindCommand(cmd, func() (int, error) {
		c, err := commandConfig(f, *config)
		if err != nil {
			return 2, err
		}
		if !explicitFlag(f, "keep") && c.Backup.Retention.Keep != nil {
			*keep = *c.Backup.Retention.Keep
		}
		if !explicitFlag(f, "older-than") && c.Backup.Retention.OlderThan != "" {
			*age, err = time.ParseDuration(c.Backup.Retention.OlderThan)
			if err != nil {
				return 2, errors.New("backup.retention.older_than 必须是时长，例如 720h")
			}
		}
		if f.NArg() != 0 || *directory == "" || *keep < 1 || *age <= 0 {
			return 2, errors.New("需要有效 directory、keep>=1、正 older-than")
		}
		root, err := filepath.Abs(*directory)
		if err != nil {
			return 2, err
		}
		if action == "verify" {
			manifest, err := verifiedBackupTask(root)
			if err != nil {
				return 1, err
			}
			return 0, json.NewEncoder(out).Encode(manifest)
		}
		// Reject a root traversing any symlink, including junctions resolved by EvalSymlinks.
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			return 1, err
		}
		if filepath.Clean(resolved) != filepath.Clean(root) {
			return 2, errors.New("清理根目录不能经过符号链接")
		}
		if !*dry {
			lock, err := os.OpenFile(filepath.Join(root, ".opskit-prune.lock"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return 1, fmt.Errorf("清理锁已存在或目录不可写: %w", err)
			}
			lock.Close()
			defer os.Remove(filepath.Join(root, ".opskit-prune.lock"))
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return 1, err
		}
		type candidate struct {
			path     string
			manifest Manifest
		}
		var candidates []candidate
		protected := []string{}
		for _, entry := range entries {
			if entry.Name() == ".opskit-prune.lock" {
				continue
			}
			path := filepath.Join(root, entry.Name())
			manifest, err := verifiedBackupTask(path)
			if err != nil || (manifest.Upload != "not-requested" && manifest.Upload != "verified-metadata") {
				protected = append(protected, entry.Name())
				continue
			}
			candidates = append(candidates, candidate{path, manifest})
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].manifest.Created.Equal(candidates[j].manifest.Created) {
				return candidates[i].path < candidates[j].path
			}
			return candidates[i].manifest.Created.After(candidates[j].manifest.Created)
		})
		selected := []string{}
		cutoff := time.Now().Add(-*age)
		for i, candidate := range candidates {
			if i < *keep || !candidate.manifest.Created.Before(cutoff) {
				continue
			}
			if err := parent.Err(); err != nil {
				return 4, err
			}
			selected = append(selected, filepath.Base(candidate.path))
			if !*dry {
				// Revalidate immediately before deleting the two known files; never recursively delete.
				current, err := verifiedBackupTask(candidate.path)
				if err != nil || current.SHA256 != candidate.manifest.SHA256 || current.Upload != candidate.manifest.Upload {
					return 1, errors.New("备份在清理过程中发生变化，停止")
				}
				if err := os.Remove(filepath.Join(candidate.path, current.File)); err != nil {
					return 1, err
				}
				if err := os.Remove(filepath.Join(candidate.path, "manifest.json")); err != nil {
					return 1, err
				}
				if err := os.Remove(candidate.path); err != nil {
					return 1, err
				}
			}
		}
		return 0, json.NewEncoder(out).Encode(struct {
			SchemaVersion int      `json:"schema_version"`
			DryRun        bool     `json:"dry_run"`
			Selected      []string `json:"selected"`
			Protected     []string `json:"protected"`
		}{1, *dry, selected, protected})
	})
	return cmd, nil
}
