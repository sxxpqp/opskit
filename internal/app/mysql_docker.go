package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"time"
)

func mysqlDockerPlanValue(o mysqlBackupOptions, value string) string {
	if o.clientMode == "docker" {
		return value
	}
	return ""
}

func (o mysqlBackupOptions) validateClientMode() error {
	if o.clientMode == "local" {
		return nil
	}
	if o.clientMode != "docker" {
		return errors.New("client-mode 必须为 local / docker")
	}
	if o.image == "" || strings.HasPrefix(o.image, "-") || strings.ContainsAny(o.image, " \t\r\n\x00") {
		return errors.New("Docker 模式必须配置有效的 mysql.docker.image 或 --image")
	}
	if o.dockerBinary == "" || o.dockerNetwork == "" || strings.ContainsAny(o.dockerNetwork, " \t\r\n\x00") || strings.HasPrefix(o.dockerNetwork, "-") {
		return errors.New("Docker binary / network 无效")
	}
	if o.pullPolicy != "missing" && o.pullPolicy != "always" && o.pullPolicy != "never" {
		return errors.New("pull-policy 必须为 missing / always / never")
	}
	if o.dockerNetwork == "bridge" && (strings.EqualFold(o.host, "localhost") || net.ParseIP(o.host).IsLoopback()) {
		return errors.New("Docker bridge 中的 localhost/127.0.0.1 指向客户端容器；请设置可达 --host，Docker Desktop 可用 host.docker.internal，Linux 宿主机可选 --docker-network host")
	}
	return nil
}

// --mount is parsed as CSV by Docker. Quote the entire source field (including commas on Windows).
func mysqlAuthMount(auth string) string {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"type=bind", "source=" + auth, "target=/opskit-auth", "readonly"})
	w.Flush()
	return strings.TrimSuffix(b.String(), "\n")
}

// Docker Desktop bind files can appear world-writable in Linux even with a private host ACL.
// MySQL ignores such option files. Stage secrets in tmpfs with native 0600 permissions.
// This script is fixed: all client options remain separate argv entries, never shell text.
const mysqlContainerBootstrap = `set -eu; umask 077; cp /opskit-auth/* /opskit-client/; chmod 0600 /opskit-client/*; exec "$@"`

func runMySQLClient(ctx context.Context, o mysqlBackupOptions, tool string, args, env []string, auth string, out io.Writer) error {
	binary := o.client
	if tool == "mysqldump" {
		binary = o.dump
	}
	if o.clientMode != "docker" {
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env, cmd.Stdout, cmd.Stderr = env, out, io.Discard
		return cmd.Run()
	}
	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		return err
	}
	name := "opskit-mysql-" + hex.EncodeToString(id)
	// Container root needs the default DAC capability to read 0700/0600 bind files owned by a non-root host user.
	runArgs := []string{"run", "--rm", "--name", name, "--pull", o.pullPolicy, "--network", o.dockerNetwork, "--user", "0:0", "--read-only", "--security-opt", "no-new-privileges:true", "--mount", mysqlAuthMount(auth), "--tmpfs", "/opskit-client:rw,noexec,nosuid,size=16m,mode=0700", "--env", "MYSQL_TEST_LOGIN_FILE=/opskit-client/unused.login", "--entrypoint", "/bin/sh", o.image, "-c", mysqlContainerBootstrap, "opskit-mysql", tool}
	runArgs = append(runArgs, args...)
	cmd := exec.CommandContext(ctx, o.dockerBinary, runArgs...)
	cmd.Env, cmd.Stdout, cmd.Stderr = env, out, io.Discard
	err := cmd.Run()
	if err != nil || ctx.Err() != nil {
		// Killing the attached Docker CLI does not guarantee the container stopped.
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rm := exec.CommandContext(cleanup, o.dockerBinary, "rm", "--force", name)
		rm.Env, rm.Stdout, rm.Stderr = env, io.Discard, io.Discard
		if rmErr := rm.Run(); rmErr != nil {
			// --rm may have removed it already. Distinguish that from a daemon failure.
			ps := exec.CommandContext(cleanup, o.dockerBinary, "ps", "--all", "--filter", "name=^/"+name+"$", "--format", "{{.Names}}")
			ps.Env, ps.Stderr = env, io.Discard
			raw, checkErr := ps.Output()
			if checkErr != nil || len(bytes.TrimSpace(raw)) != 0 {
				return &mysqlContainerCleanupError{name: name}
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return err
}

type mysqlContainerCleanupError struct{ name string }

func (e *mysqlContainerCleanupError) Error() string {
	return fmt.Sprintf("无法确认临时容器 %s 已删除；请检查 Docker 引擎并清理该容器", e.name)
}

func mysqlClientFailure(message string, err error) error {
	var cleanup *mysqlContainerCleanupError
	if errors.As(err, &cleanup) {
		return errors.Join(errors.New(message), cleanup)
	}
	return errors.New(message)
}
