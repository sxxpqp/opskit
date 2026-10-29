package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func middlewareIdentity(c middlewareSettings) []byte {
	component := c.component()
	addresses := []string{}
	for _, host := range c.Hosts {
		addresses = append(addresses, host.InternalAddress)
	}
	spec, _ := json.Marshal(struct {
		Image, ShellImage, RouterImage, Username, BindAddress, FrontendImage string
		PublishPort                                                          int
		Addresses                                                            []string
	}{component.Image, component.ShellImage, component.RouterImage, component.Username, c.BindAddress, component.FrontendImage, c.PublishPort, addresses})
	raw, _ := json.Marshal(struct {
		Name   string `json:"name"`
		Engine string `json:"engine"`
		Driver string `json:"driver"`
		Spec   string `json:"spec_sha256"`
	}{c.Name, c.Engine, c.Driver, fmt.Sprintf("%x", sha256.Sum256(spec))})
	return raw
}

func middlewareHostBundle(c middlewareSettings, b middlewareBundle, index int) (middlewareBundle, error) {
	var document map[string]any
	if err := yaml.Unmarshal(b.Files["compose.yaml"], &document); err != nil {
		return b, err
	}
	services := document["services"].(map[string]any)
	wanted := map[string]bool{"bootstrap": true, "check": true, "router": true, "frontend": true, "recover": true}
	for _, node := range b.Nodes {
		if node["host_index"].(int) == index {
			wanted[node["service"].(string)] = true
		}
	}
	for name := range services {
		if !wanted[name] {
			delete(services, name)
		}
	}
	files := map[string][]byte{}
	for name, content := range b.Files {
		files[name] = content
	}
	raw, err := yaml.Marshal(document)
	if err != nil {
		return b, err
	}
	files["compose.yaml"] = raw
	b.Files = files
	return b, nil
}

func deployMiddlewareSSH(ctx context.Context, c middlewareSettings, b middlewareBundle, action string, out io.Writer) error {
	return executeMiddlewareSSH(ctx, c, b, action, out, dialClusterSSH)
}
func executeMiddlewareSSH(ctx context.Context, c middlewareSettings, b middlewareBundle, action string, out io.Writer, dial func(context.Context, clusterHost) (clusterRemote, error)) error {
	var remotes []clusterRemote
	defer func() {
		for _, r := range remotes {
			r.Close()
		}
	}()
	// All hosts pass read-only checks before any credentials or configuration are uploaded.
	for _, host := range c.Hosts {
		r, err := dial(ctx, host)
		if err != nil {
			return fmt.Errorf("%s: SSH 连接失败: %w", host.Name, err)
		}
		remotes = append(remotes, r)
		raw, err := r.Exec(ctx, true, `set -eu; test "$(uname -s)" = Linux; docker info >/dev/null; docker compose version >/dev/null; printf ready`)
		if err != nil || string(raw) != "ready" {
			if err == nil {
				err = errors.New("系统预检未返回 ready")
			}
			return fmt.Errorf("%s: 需要可用的 Linux Docker Engine、Compose 和 sudo: %w", host.Name, err)
		}
	}
	root := "/var/lib/opskit/middleware/" + c.project()
	compose := "env -u OPSKIT_CLUSTER_PASSWORD docker compose --project-name " + shellQuote(c.project()) + " --project-directory " + shellQuote(root) + " -f " + shellQuote(root+"/compose.yaml")
	secret := ""
	if action == "deploy" {
		var err error
		secret, err = middlewarePassword(c)
		if err != nil {
			return err
		}
	}
	clean := func(s string) string {
		if secret != "" {
			return strings.ReplaceAll(s, secret, "<password>")
		}
		return s
	}
	run := func(index int, phase, command string) error {
		fmt.Fprintf(out, "[%s/%s] 开始\n", c.Hosts[index].Name, phase)
		raw, err := remotes[index].Exec(ctx, true, command)
		if len(raw) > 0 {
			fmt.Fprintln(out, clean(string(raw)))
		}
		if err != nil {
			return fmt.Errorf("%s/%s 失败；保留数据，修复环境后按相同配置重跑: %s", c.Hosts[index].Name, phase, clean(err.Error()))
		}
		return nil
	}
	if action == "status" {
		return run(0, "cluster-check", compose+" run --rm --no-deps check")
	}
	for i, r := range remotes {
		lockPath := root + ".deploy.lock"
		if err := run(i, "lock", "set -eu; umask 077; mkdir -p /var/lib/opskit/middleware; mkdir -- "+shellQuote(lockPath)); err != nil {
			return err
		}
		defer func(r clusterRemote, path string) {
			cleanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = r.Exec(cleanCtx, true, "rmdir -- "+shellQuote(path))
		}(r, lockPath)
	}
	if action == "recover" {
		if c.Engine != "mysql" {
			return errors.New("recover 只支持 MySQL 完整停机恢复")
		}
		for i := range remotes {
			if err := run(i, "ownership", "printf '%s' "+shellQuote(string(middlewareIdentity(c)))+" | cmp - "+shellQuote(root+"/ownership.json")); err != nil {
				return err
			}
		}
		if err := run(0, "recover", compose+" run --rm --no-deps recover"); err != nil {
			return err
		}
		for i := range remotes {
			if err := run(i, "router", compose+" up -d router"); err != nil {
				return err
			}
		}
		return run(0, "cluster-check", compose+" run --rm --no-deps check")
	}
	for i, r := range remotes {
		nodeBundle, err := middlewareHostBundle(c, b, i)
		if err != nil {
			return err
		}
		raw, err := r.Exec(ctx, false, "umask 077; mktemp -d /tmp/opskit-middleware.XXXXXXXX")
		if err != nil {
			return err
		}
		temp := strings.TrimSpace(string(raw))
		if !strings.HasPrefix(temp, "/tmp/opskit-middleware.") || strings.ContainsAny(temp, "\n\r ';") {
			return errors.New("远端临时目录无效")
		}
		defer func(r clusterRemote, path string) {
			cleanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = r.Exec(cleanCtx, true, "rm -rf -- "+shellQuote(path))
		}(r, temp)
		nodeBundle.Files["ownership.json"] = middlewareIdentity(c)
		nodeBundle.Files[".env"] = []byte("OPSKIT_CLUSTER_PASSWORD=" + secret + "\n")
		for name, content := range nodeBundle.Files {
			if err := r.Upload(ctx, temp+"/"+name, bytes.NewReader(content)); err != nil {
				return err
			}
		}
		check := `set -eu; if test -e ` + shellQuote(root) + `; then test -f ` + shellQuote(root+"/ownership.json") + `; cmp -s ` + shellQuote(root+"/ownership.json") + " " + shellQuote(temp+"/ownership.json") + `; if test -e ` + shellQuote(root+"/.env") + `; then cmp -s ` + shellQuote(root+"/.env") + " " + shellQuote(temp+"/.env") + `; fi; else test -z "$(docker ps -aq --filter ` + shellQuote("label=com.docker.compose.project="+c.project()) + `)"; test -z "$(docker volume ls -q --filter ` + shellQuote("name=^"+c.project()+"_") + `)"; install -d -m 700 ` + shellQuote(root) + `; fi`
		if err := run(i, "ownership", check); err != nil {
			return err
		}
		for name := range nodeBundle.Files {
			mode := "644"
			if name == ".env" || name == "ownership.json" {
				mode = "600"
			}
			if _, err := r.Exec(ctx, true, "install -m "+mode+" "+shellQuote(temp+"/"+name)+" "+shellQuote(root+"/"+name)); err != nil {
				return err
			}
		}
	}
	// Start every host before waiting: distributed MinIO needs peer processes concurrently.
	for i := range remotes {
		if err := run(i, "start", compose+" up -d"); err != nil {
			return err
		}
	}
	for i := range remotes {
		if err := run(i, "health", compose+" up -d --wait --wait-timeout 600"); err != nil {
			return err
		}
	}
	if b.Bootstrap != "" {
		if err := run(0, "bootstrap", compose+" run --rm --no-deps bootstrap"); err != nil {
			return err
		}
	}
	if c.Engine == "mysql" {
		for i := range remotes {
			if err := run(i, "router", compose+" up -d router"); err != nil {
				return err
			}
		}
	}
	if c.Engine == "minio" {
		for i := range remotes {
			if err := run(i, "frontend-reload", compose+" exec -T frontend haproxy -c -f /usr/local/etc/haproxy/haproxy.cfg && "+compose+" kill --signal USR2 frontend"); err != nil {
				return err
			}
		}
	}
	return run(0, "cluster-check", compose+" run --rm --no-deps check")
}
