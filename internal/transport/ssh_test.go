package transport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// testServer is a minimal SSH server that runs exec requests with the local
// /bin/sh and forwards direct-tcpip channels, which is enough to exercise
// the client end to end.
type testServer struct {
	addr    string
	hostKey ssh.PublicKey

	mu    sync.Mutex
	conns []*ssh.ServerConn
}

func startTestServer(t *testing.T, password string, authorized ssh.PublicKey) *testServer {
	t.Helper()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if password != "" && string(pass) == password {
				return nil, nil
			}
			return nil, errors.New("wrong password")
		},
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if authorized != nil && string(key.Marshal()) == string(authorized.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("unknown key")
		},
	}
	cfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	srv := &testServer{addr: ln.Addr().String(), hostKey: hostSigner.PublicKey()}
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.serve(nc, cfg)
		}
	}()
	return srv
}

func (s *testServer) host() string {
	host, _, _ := net.SplitHostPort(s.addr)
	return host
}

func (s *testServer) port() int {
	_, port, _ := net.SplitHostPort(s.addr)
	n, _ := strconv.Atoi(port)
	return n
}

// dropConnections closes every client connection, as a server restart would.
func (s *testServer) dropConnections() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, conn := range s.conns {
		_ = conn.Close()
	}
	s.conns = nil
}

func (s *testServer) serve(nc net.Conn, cfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		_ = nc.Close()
		return
	}
	s.mu.Lock()
	s.conns = append(s.conns, conn)
	s.mu.Unlock()
	defer func() { _ = conn.Close() }()
	go ssh.DiscardRequests(reqs)

	for newCh := range chans {
		switch newCh.ChannelType() {
		case "session":
			ch, chReqs, err := newCh.Accept()
			if err != nil {
				continue
			}
			go serveSession(ch, chReqs)
		case "direct-tcpip":
			var target struct {
				Host       string
				Port       uint32
				OriginHost string
				OriginPort uint32
			}
			if err := ssh.Unmarshal(newCh.ExtraData(), &target); err != nil {
				_ = newCh.Reject(ssh.ConnectionFailed, err.Error())
				continue
			}
			upstream, err := net.Dial("tcp", net.JoinHostPort(target.Host, strconv.Itoa(int(target.Port))))
			if err != nil {
				_ = newCh.Reject(ssh.ConnectionFailed, err.Error())
				continue
			}
			ch, chReqs, err := newCh.Accept()
			if err != nil {
				_ = upstream.Close()
				continue
			}
			go ssh.DiscardRequests(chReqs)
			go func() {
				defer func() { _ = ch.Close() }()
				defer func() { _ = upstream.Close() }()
				go func() { _, _ = io.Copy(upstream, ch) }()
				_, _ = io.Copy(ch, upstream)
			}()
		default:
			_ = newCh.Reject(ssh.UnknownChannelType, "unsupported")
		}
	}
}

func serveSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer func() { _ = ch.Close() }()
	for req := range reqs {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			_ = req.Reply(false, nil)
			return
		}
		_ = req.Reply(true, nil)

		cmd := exec.Command("/bin/sh", "-c", payload.Command)
		cmd.Stdin = ch
		cmd.Stdout = ch
		cmd.Stderr = ch.Stderr()
		status := 0
		var exitErr *exec.ExitError
		if err := cmd.Run(); errors.As(err, &exitErr) {
			status = exitErr.ExitCode()
		} else if err != nil {
			status = 255
		}
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(status)}))
		return
	}
}

func TestSSHPasswordAuth(t *testing.T) {
	srv := startTestServer(t, "s3cret", nil)
	runner := NewSSH(SSHConfig{
		Host: srv.host(), Port: srv.port(), User: "test",
		Password: "s3cret", InsecureIgnoreHostKey: true,
	}, 0)
	t.Cleanup(func() { _ = runner.Close() })

	res, err := runner.Run(context.Background(), `printf 'out:%s' "$(cat)"; echo oops >&2`, strings.NewReader("in put"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(res.Stdout); got != "out:in put" {
		t.Errorf("stdout = %q", got)
	}
	if got := string(res.Stderr); got != "oops\n" {
		t.Errorf("stderr = %q", got)
	}

	_, err = runner.Run(context.Background(), "echo failing >&2; exit 3", nil)
	if code, ok := ExitCode(err); !ok || code != 3 {
		t.Fatalf("expected exit code 3, got %v", err)
	}
	if !strings.Contains(err.Error(), "failing") {
		t.Errorf("error %q does not include stderr", err)
	}
}

func TestSSHWrongPassword(t *testing.T) {
	srv := startTestServer(t, "s3cret", nil)
	runner := NewSSH(SSHConfig{
		Host: srv.host(), Port: srv.port(), User: "test",
		Password: "nope", InsecureIgnoreHostKey: true, Timeout: 5 * time.Second,
	}, 0)
	t.Cleanup(func() { _ = runner.Close() })

	if _, err := runner.Run(context.Background(), "true", nil); err == nil {
		t.Fatal("expected an authentication error")
	}
}

func TestSSHPrivateKeyAuth(t *testing.T) {
	_, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientPriv)
	if err != nil {
		t.Fatal(err)
	}
	srv := startTestServer(t, "", clientSigner.PublicKey())

	for _, passphrase := range []string{"", "pass phrase"} {
		t.Run(fmt.Sprintf("passphrase=%q", passphrase), func(t *testing.T) {
			var block *pem.Block
			if passphrase == "" {
				block, err = ssh.MarshalPrivateKey(clientPriv, "")
			} else {
				block, err = ssh.MarshalPrivateKeyWithPassphrase(clientPriv, "", []byte(passphrase))
			}
			if err != nil {
				t.Fatal(err)
			}
			runner := NewSSH(SSHConfig{
				Host: srv.host(), Port: srv.port(), User: "test",
				PrivateKey:           string(pem.EncodeToMemory(block)),
				PrivateKeyPassphrase: passphrase,
				HostKey:              string(ssh.MarshalAuthorizedKey(srv.hostKey)),
			}, 0)
			t.Cleanup(func() { _ = runner.Close() })

			res, err := runner.Run(context.Background(), "echo ok", nil)
			if err != nil {
				t.Fatal(err)
			}
			if string(res.Stdout) != "ok\n" {
				t.Errorf("stdout = %q", res.Stdout)
			}
		})
	}
}

func TestSSHEncryptedKeyWithoutPassphrase(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := SSHConfig{PrivateKey: string(pem.EncodeToMemory(block))}
	if _, _, err := cfg.authMethods(); err == nil || !strings.Contains(err.Error(), "passphrase") {
		t.Fatalf("expected a passphrase error, got %v", err)
	}
}

func TestSSHHostKeyVerification(t *testing.T) {
	srv := startTestServer(t, "pw", nil)
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	otherSigner, _ := ssh.NewSignerFromKey(otherPriv)

	knownHostsWith := func(t *testing.T, keys ...ssh.PublicKey) string {
		t.Helper()
		var lines []string
		for _, key := range keys {
			lines = append(lines, knownhosts.Line([]string{knownhosts.Normalize(srv.addr)}, key))
		}
		file := filepath.Join(t.TempDir(), "known_hosts")
		if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return file
	}

	tests := []struct {
		name    string
		cfg     SSHConfig
		wantErr string
	}{
		{name: "pinned key", cfg: SSHConfig{HostKey: string(ssh.MarshalAuthorizedKey(srv.hostKey))}},
		{name: "pinned wrong key", cfg: SSHConfig{HostKey: string(ssh.MarshalAuthorizedKey(otherSigner.PublicKey()))}, wantErr: "handshake failed"},
		{name: "known host", cfg: SSHConfig{KnownHostsFile: knownHostsWith(t, srv.hostKey)}},
		{name: "unknown host", cfg: SSHConfig{KnownHostsFile: knownHostsWith(t)}, wantErr: "ssh-keyscan"},
		{name: "changed host key", cfg: SSHConfig{KnownHostsFile: knownHostsWith(t, otherSigner.PublicKey())}, wantErr: "host key mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			cfg.Host, cfg.Port, cfg.User, cfg.Password = srv.host(), srv.port(), "test", "pw"
			cfg.Timeout = 5 * time.Second
			runner := NewSSH(cfg, 0)
			t.Cleanup(func() { _ = runner.Close() })

			_, err := runner.Run(context.Background(), "true", nil)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestSSHBastion(t *testing.T) {
	bastion := startTestServer(t, "bastion-pw", nil)
	target := startTestServer(t, "target-pw", nil)

	runner := NewSSH(SSHConfig{
		Host: target.host(), Port: target.port(), User: "test",
		Password: "target-pw", HostKey: string(ssh.MarshalAuthorizedKey(target.hostKey)),
		Bastion: &SSHConfig{
			Host: bastion.host(), Port: bastion.port(), User: "jump",
			Password: "bastion-pw", HostKey: string(ssh.MarshalAuthorizedKey(bastion.hostKey)),
		},
	}, 0)
	t.Cleanup(func() { _ = runner.Close() })

	res, err := runner.Run(context.Background(), "echo through", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "through\n" {
		t.Errorf("stdout = %q", res.Stdout)
	}
}

func TestSSHConcurrentSessions(t *testing.T) {
	srv := startTestServer(t, "pw", nil)
	runner := NewSSH(SSHConfig{
		Host: srv.host(), Port: srv.port(), User: "test",
		Password: "pw", InsecureIgnoreHostKey: true,
	}, 3)
	t.Cleanup(func() { _ = runner.Close() })

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := runner.Run(context.Background(), fmt.Sprintf("echo %d", i), nil)
			if err != nil {
				errs <- err
				return
			}
			if got := strings.TrimSpace(string(res.Stdout)); got != strconv.Itoa(i) {
				errs <- fmt.Errorf("run %d: stdout = %q", i, got)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestSSHReconnects(t *testing.T) {
	srv := startTestServer(t, "pw", nil)
	runner := NewSSH(SSHConfig{
		Host: srv.host(), Port: srv.port(), User: "test",
		Password: "pw", InsecureIgnoreHostKey: true,
	}, 0)
	t.Cleanup(func() { _ = runner.Close() })

	if _, err := runner.Run(context.Background(), "true", nil); err != nil {
		t.Fatal(err)
	}
	srv.dropConnections()
	// Let the client notice the connection is gone.
	time.Sleep(100 * time.Millisecond)
	if _, err := runner.Run(context.Background(), "true", nil); err != nil {
		t.Fatalf("expected the runner to reconnect, got %v", err)
	}
}

func TestSSHCancel(t *testing.T) {
	srv := startTestServer(t, "pw", nil)
	runner := NewSSH(SSHConfig{
		Host: srv.host(), Port: srv.port(), User: "test",
		Password: "pw", InsecureIgnoreHostKey: true,
	}, 0)
	t.Cleanup(func() { _ = runner.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := runner.Run(ctx, "sleep 10", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected a deadline error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}
