//go:build !windows

package app

import "os"

func restrictMySQLDirectory(path string) error { return os.Chmod(path, 0700) }
