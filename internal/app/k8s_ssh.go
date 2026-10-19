package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type clusterRemote interface {
	Exec(context.Context, bool, string) ([]byte, error)
	Upload(context.Context, string, io.Reader) error
	Close() error
}
type sshClusterRemote struct {
	client       *ssh.Client
	host         clusterHost
	password     string
	sudoPassword string
	agentConn    net.Conn
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func dialClusterSSH(ctx context.Context, h clusterHost) (clusterRemote, error) {
	password, err := secretValue(h.Password, h.PasswordEnv)
	if err != nil {
		return nil, err
	}
	sudo, err := secretValue(h.SudoPassword, h.SudoPasswordEnv)
	if err != nil {
		return nil, err
	}
	var methods []ssh.AuthMethod
	var agentConn net.Conn
	if password != "" {
		methods = append(methods, ssh.Password(password))
	} else if h.PrivateKeyPath != "" {
		data, err := os.ReadFile(h.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("读取 SSH 私钥: %w", err)
		}
		var signer ssh.Signer
		if h.PrivateKeyPassphraseEnv != "" {
			pass, err := secretValue("", h.PrivateKeyPassphraseEnv)
			if err != nil {
				return nil, err
			}
			signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(pass))
			if err != nil {
				return nil, fmt.Errorf("解析 SSH 私钥失败")
			}
		} else {
			signer, err = ssh.ParsePrivateKey(data)
			if err != nil {
				return nil, fmt.Errorf("解析 SSH 私钥失败（加密私钥请配置 privateKeyPassphraseEnv）")
			}
		}
		methods = append(methods, ssh.PublicKeys(signer))
	} else {
		socket := os.Getenv("SSH_AUTH_SOCK")
		if socket == "" {
			return nil, fmt.Errorf("%s: 未配置 SSH 密码/私钥，且没有 SSH Agent", h.Name)
		}
		agentConn, err = net.DialTimeout("unix", socket, 10*time.Second)
		if err != nil {
			return nil, err
		}
		methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(agentConn).Signers))
	}
	address := net.JoinHostPort(h.Address, fmt.Sprint(h.Port))
	dialer := net.Dialer{Timeout: 15 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		if agentConn != nil {
			agentConn.Close()
		}
		return nil, err
	}
	deadline := time.Now().Add(20 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	clientConn, channels, requests, err := ssh.NewClientConn(conn, address, &ssh.ClientConfig{User: h.User, Auth: methods, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 15 * time.Second})
	if err != nil {
		conn.Close()
		if agentConn != nil {
			agentConn.Close()
		}
		return nil, fmt.Errorf("%s: SSH 连接或认证失败: %w", h.Name, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return &sshClusterRemote{client: ssh.NewClient(clientConn, channels, requests), host: h, password: password, sudoPassword: sudo, agentConn: agentConn}, nil
}
func (r *sshClusterRemote) Close() error {
	if r.agentConn != nil {
		r.agentConn.Close()
	}
	return r.client.Close()
}
func (r *sshClusterRemote) run(ctx context.Context, command string, input io.Reader) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, err := r.client.NewSession()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var out, diag bytes.Buffer
	s.Stdout = &out
	s.Stderr = &diag
	s.Stdin = input
	if err = s.Start(command); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- s.Wait() }()
	select {
	case <-ctx.Done():
		_ = s.Signal(ssh.SIGTERM)
		_ = s.Close()
		return nil, ctx.Err()
	case err = <-done:
	}
	if err != nil {
		return out.Bytes(), fmt.Errorf("远端命令失败: %w: %s", err, r.redact(diag.String()))
	}
	return out.Bytes(), nil
}
func (r *sshClusterRemote) redact(text string) string {
	for _, secret := range []string{r.password, r.sudoPassword} {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "<redacted>")
		}
	}
	return text
}
func (r *sshClusterRemote) Exec(ctx context.Context, root bool, command string) ([]byte, error) {
	var input io.Reader
	if root && r.host.User != "root" {
		if r.sudoPassword != "" {
			command = "sudo -k -S -p '' -- sh -c " + shellQuote(command)
			input = strings.NewReader(r.sudoPassword + "\n")
		} else {
			command = "sudo -n -- sh -c " + shellQuote(command)
		}
	}
	return r.run(ctx, command, input)
}
func (r *sshClusterRemote) Upload(ctx context.Context, path string, src io.Reader) error {
	_, err := r.run(ctx, "umask 077; cat > "+shellQuote(path), src)
	return err
}
