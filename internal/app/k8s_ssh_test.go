package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestClusterSSHPasswordSudoWithoutHostKeyVerification(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if c.User() != "ubuntu" || string(password) != "ssh-password" {
			return nil, fmt.Errorf("bad password")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	type result struct{ command, input string }
	observed := make(chan result, 8)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				server, channels, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					conn.Close()
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for channel := range channels {
					ch, requests, err := channel.Accept()
					if err != nil {
						return
					}
					go func() {
						defer ch.Close()
						for request := range requests {
							if request.Type != "exec" {
								_ = request.Reply(false, nil)
								continue
							}
							var payload struct{ Command string }
							if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
								return
							}
							_ = request.Reply(true, nil)
							raw, _ := io.ReadAll(ch)
							observed <- result{payload.Command, string(raw)}
							_, _ = io.WriteString(ch, "ok\n")
							_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
							return
						}
					}()
				}
			}()
		}
	}()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".ssh", "known_hosts")
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	number, _ := strconv.Atoi(port)
	h := clusterHost{Name: "node", Address: host, Port: number, User: "ubuntu", Password: "ssh-password", SudoPassword: "sudo-password"}
	remote, err := dialClusterSSH(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	raw, err := remote.Exec(context.Background(), true, "id -u")
	if err != nil || string(raw) != "ok\n" {
		t.Fatal(string(raw), err)
	}
	first := <-observed
	if !strings.Contains(first.command, "sudo -k -S -p '' -- sh -c 'id -u'") || first.input != "sudo-password\n" {
		t.Fatal("sudo credentials were not provided over stdin")
	}
	if strings.Contains(first.command, "sudo-password") {
		t.Fatal("sudo password leaked into command")
	}
	if err = remote.Upload(context.Background(), "/tmp/file with quote'", strings.NewReader("private payload")); err != nil {
		t.Fatal(err)
	}
	second := <-observed
	if second.input != "private payload" || !strings.Contains(second.command, "umask 077") {
		t.Fatal("private upload failed")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("known_hosts unexpectedly created")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	_, other, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := ssh.NewSignerFromKey(other)
	if err != nil {
		t.Fatal(err)
	}
	for _, stored := range []string{"malformed known_hosts\n", knownhosts.Line([]string{knownhosts.Normalize(listener.Addr().String())}, wrong.PublicKey()) + "\n"} {
		if err := os.WriteFile(path, []byte(stored), 0600); err != nil {
			t.Fatal(err)
		}
		client, err := dialClusterSSH(context.Background(), h)
		if err != nil {
			t.Fatal("KK-style connection consulted known_hosts", err)
		}
		client.Close()
		after, err := os.ReadFile(path)
		if err != nil || string(after) != stored {
			t.Fatal("known_hosts modified", err)
		}
	}
	bad := h
	bad.Password = "wrong-password"
	if client, err := dialClusterSSH(context.Background(), bad); err == nil {
		client.Close()
		t.Fatal("invalid SSH credentials accepted")
	}
}
