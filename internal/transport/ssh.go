package transport

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	// DefaultSSHPort is the port used when none is configured.
	DefaultSSHPort = 22
	// DefaultSSHTimeout bounds how long establishing a connection may take.
	DefaultSSHTimeout = 30 * time.Second
	// DefaultMaxSessions stays below the OpenSSH MaxSessions default (10)
	// so that Terraform's parallelism never exhausts the server's limit.
	DefaultMaxSessions = 5

	keepaliveInterval = 30 * time.Second
)

// SSHConfig describes how to reach and authenticate to a host over SSH.
type SSHConfig struct {
	Host string
	Port int
	User string

	Password             string
	PrivateKey           string
	PrivateKeyPassphrase string
	// Agent enables authentication through the agent at $SSH_AUTH_SOCK.
	Agent bool

	// HostKey pins the host key, in authorized_keys format. When empty, the
	// key is checked against KnownHostsFile unless InsecureIgnoreHostKey.
	HostKey               string
	KnownHostsFile        string
	InsecureIgnoreHostKey bool

	Timeout time.Duration

	// Bastion, when set, is the jump host used to reach Host.
	Bastion *SSHConfig
}

func (c *SSHConfig) address() string {
	port := c.Port
	if port == 0 {
		port = DefaultSSHPort
	}
	return net.JoinHostPort(c.Host, strconv.Itoa(port))
}

func (c *SSHConfig) timeout() time.Duration {
	if c.Timeout <= 0 {
		return DefaultSSHTimeout
	}
	return c.Timeout
}

// SSH runs scripts on a remote host. Every script gets its own session on a
// single shared connection, which is opened on first use and re-established
// if it breaks.
type SSH struct {
	cfg      SSHConfig
	sessions chan struct{}

	mu   sync.Mutex
	conn *sshConn
}

// NewSSH returns a Runner for the host described by cfg, running at most
// maxSessions scripts concurrently.
func NewSSH(cfg SSHConfig, maxSessions int) *SSH {
	if maxSessions <= 0 {
		maxSessions = DefaultMaxSessions
	}
	return &SSH{cfg: cfg, sessions: make(chan struct{}, maxSessions)}
}

func (s *SSH) String() string {
	return fmt.Sprintf("ssh://%s@%s", s.cfg.User, s.cfg.address())
}

func (s *SSH) Run(ctx context.Context, script string, stdin io.Reader) (*Result, error) {
	select {
	case s.sessions <- struct{}{}:
		defer func() { <-s.sessions }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	session, err := s.newSession(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()

	var stdout, stderr bytes.Buffer
	session.Stdin = stdin
	session.Stdout = &stdout
	session.Stderr = &stderr

	stop := context.AfterFunc(ctx, func() {
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
	})
	defer stop()

	// The command is interpreted by the login shell of the remote user,
	// which may not be POSIX: always hand the script over to /bin/sh.
	err = session.Run("/bin/sh -c " + Quote(script))
	res := &Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err != nil && ctx.Err() != nil {
		return res, ctx.Err()
	}
	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitStatus()
		if exitErr.Signal() != "" {
			res.ExitCode = -1
		}
		return res, &ExitError{Result: res}
	}
	if err != nil {
		return res, fmt.Errorf("%s: %w", s, err)
	}
	return res, nil
}

func (s *SSH) Close() error {
	s.mu.Lock()
	conn := s.conn
	s.conn = nil
	s.mu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}

func (s *SSH) newSession(ctx context.Context) (*ssh.Session, error) {
	conn, err := s.connection(ctx)
	if err != nil {
		return nil, err
	}
	session, err := conn.client.NewSession()
	if err == nil {
		return session, nil
	}
	var openErr *ssh.OpenChannelError
	if errors.As(err, &openErr) {
		// The server refused the session but the connection is fine.
		return nil, fmt.Errorf("%s: opening session: %w", s, err)
	}

	// The connection is most likely broken (idle timeout, server restart):
	// reconnect once.
	s.drop(conn)
	conn, err = s.connection(ctx)
	if err != nil {
		return nil, err
	}
	session, err = conn.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("%s: opening session: %w", s, err)
	}
	return session, nil
}

func (s *SSH) connection(ctx context.Context) (*sshConn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		return s.conn, nil
	}
	conn, err := dial(ctx, &s.cfg)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s, err)
	}
	s.conn = conn
	go s.keepalive(conn)
	return conn, nil
}

// drop forgets conn, if it is still the current connection, and closes it.
func (s *SSH) drop(conn *sshConn) {
	s.mu.Lock()
	if s.conn == conn {
		s.conn = nil
	}
	s.mu.Unlock()
	_ = conn.Close()
}

func (s *SSH) keepalive(conn *sshConn) {
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-conn.done:
			return
		case <-ticker.C:
			if _, _, err := conn.client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				s.drop(conn)
				return
			}
		}
	}
}

type sshConn struct {
	client *ssh.Client
	// closers are released after client: the agent connection and, when
	// tunnelling, the bastion connection.
	closers []io.Closer

	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func (c *sshConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
		c.closeErr = c.client.Close()
		for _, closer := range c.closers {
			_ = closer.Close()
		}
	})
	return c.closeErr
}

func dial(ctx context.Context, cfg *SSHConfig) (_ *sshConn, err error) {
	var closers []io.Closer
	defer func() {
		if err != nil {
			for _, closer := range closers {
				_ = closer.Close()
			}
		}
	}()

	clientCfg, agentConn, err := cfg.clientConfig()
	if agentConn != nil {
		closers = append(closers, agentConn)
	}
	if err != nil {
		return nil, err
	}

	addr := cfg.address()
	var netConn net.Conn
	if cfg.Bastion != nil {
		bastion, err := dial(ctx, cfg.Bastion)
		if err != nil {
			return nil, fmt.Errorf("bastion %s: %w", cfg.Bastion.address(), err)
		}
		closers = append(closers, bastion)
		netConn, err = bastion.client.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("connecting to %s through bastion: %w", addr, err)
		}
	} else {
		dialer := net.Dialer{Timeout: cfg.timeout()}
		netConn, err = dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
	}

	// Abort the handshake if it outlives the timeout or the context. The
	// deadline is not supported on tunnelled connections, hence the timer.
	timer := time.AfterFunc(cfg.timeout(), func() { _ = netConn.Close() })
	stop := context.AfterFunc(ctx, func() { _ = netConn.Close() })
	sshConnection, chans, reqs, err := ssh.NewClientConn(netConn, addr, clientCfg)
	timer.Stop()
	stop()
	if err != nil {
		_ = netConn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}

	return &sshConn{
		client:  ssh.NewClient(sshConnection, chans, reqs),
		closers: closers,
		done:    make(chan struct{}),
	}, nil
}

// clientConfig builds the SSH client configuration. The returned closer, if
// any, is the connection to the SSH agent and must outlive the client.
func (c *SSHConfig) clientConfig() (*ssh.ClientConfig, io.Closer, error) {
	auth, agentConn, err := c.authMethods()
	if err != nil {
		return nil, agentConn, err
	}
	hostKeyCallback, algorithms, err := c.hostKeyCallback()
	if err != nil {
		return nil, agentConn, err
	}
	return &ssh.ClientConfig{
		User:              c.User,
		Auth:              auth,
		HostKeyCallback:   hostKeyCallback,
		HostKeyAlgorithms: algorithms,
		Timeout:           c.timeout(),
	}, agentConn, nil
}

func (c *SSHConfig) authMethods() ([]ssh.AuthMethod, io.Closer, error) {
	var signers []ssh.Signer
	if c.PrivateKey != "" {
		signer, err := parsePrivateKey([]byte(c.PrivateKey), c.PrivateKeyPassphrase)
		if err != nil {
			return nil, nil, err
		}
		signers = append(signers, signer)
	}

	var agentConn net.Conn
	if c.Agent {
		if socket := os.Getenv("SSH_AUTH_SOCK"); socket != "" {
			conn, err := net.Dial("unix", socket)
			if err != nil {
				return nil, nil, fmt.Errorf("connecting to SSH agent: %w", err)
			}
			agentConn = conn
			agentSigners, err := agent.NewClient(conn).Signers()
			if err != nil {
				return nil, agentConn, fmt.Errorf("listing SSH agent keys: %w", err)
			}
			signers = append(signers, agentSigners...)
		}
	}

	var methods []ssh.AuthMethod
	if len(signers) > 0 {
		methods = append(methods, ssh.PublicKeys(signers...))
	}
	if c.Password != "" {
		password := c.Password
		methods = append(methods,
			ssh.Password(password),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = password
				}
				return answers, nil
			}),
		)
	}
	if len(methods) == 0 {
		return nil, agentConn, errors.New("no SSH authentication method available: set a password or a private key, or load a key in the SSH agent")
	}
	return methods, agentConn, nil
}

func parsePrivateKey(pem []byte, passphrase string) (ssh.Signer, error) {
	if passphrase != "" {
		signer, err := ssh.ParsePrivateKeyWithPassphrase(pem, []byte(passphrase))
		if err != nil {
			return nil, fmt.Errorf("parsing private key: %w", err)
		}
		return signer, nil
	}
	signer, err := ssh.ParsePrivateKey(pem)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		return nil, errors.New("parsing private key: the key is encrypted, set its passphrase")
	}
	if err != nil {
		return nil, fmt.Errorf("parsing private key: %w", err)
	}
	return signer, nil
}

func (c *SSHConfig) hostKeyCallback() (ssh.HostKeyCallback, []string, error) {
	switch {
	case c.HostKey != "":
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(c.HostKey))
		if err != nil {
			return nil, nil, fmt.Errorf("parsing host key: %w", err)
		}
		return ssh.FixedHostKey(key), hostKeyAlgorithms(key.Type()), nil
	case c.InsecureIgnoreHostKey:
		return ssh.InsecureIgnoreHostKey(), nil, nil
	}

	file := c.KnownHostsFile
	if file == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, nil, fmt.Errorf("locating known_hosts: %w", err)
		}
		file = filepath.Join(home, ".ssh", "known_hosts")
	}
	callback, err := knownhosts.New(file)
	if err != nil {
		return nil, nil, fmt.Errorf("loading known hosts: %w", err)
	}

	// Without this, the server may offer a key type that known_hosts does
	// not list for the host, which knownhosts reports as a mismatch.
	var algorithms []string
	for _, known := range knownKeys(callback, c.address()) {
		algorithms = append(algorithms, hostKeyAlgorithms(known.Key.Type())...)
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := callback(hostname, remote, key)
		var keyErr *knownhosts.KeyError
		if !errors.As(err, &keyErr) {
			return err
		}
		if len(keyErr.Want) == 0 {
			return fmt.Errorf("host %s is not in %s (it presented the %s key %s): add it with `ssh-keyscan -p %d %s >> %s`, or set host_key",
				hostname, file, key.Type(), ssh.FingerprintSHA256(key), c.portOrDefault(), c.Host, file)
		}
		return fmt.Errorf("host key mismatch for %s: it presented the %s key %s, which does not match %s:%d; this can mean someone is intercepting the connection",
			hostname, key.Type(), ssh.FingerprintSHA256(key), keyErr.Want[0].Filename, keyErr.Want[0].Line)
	}, algorithms, nil
}

func (c *SSHConfig) portOrDefault() int {
	if c.Port == 0 {
		return DefaultSSHPort
	}
	return c.Port
}

// knownKeys returns the keys that callback knows for address.
func knownKeys(callback ssh.HostKeyCallback, address string) []knownhosts.KnownKey {
	// A key made of zeros never matches, so the callback lists what it knows.
	probe, err := ssh.NewPublicKey(ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)))
	if err != nil {
		return nil
	}
	var keyErr *knownhosts.KeyError
	if errors.As(callback(address, &net.TCPAddr{IP: net.IPv4zero}, probe), &keyErr) {
		return keyErr.Want
	}
	return nil
}

func hostKeyAlgorithms(keyType string) []string {
	if keyType == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	return []string{keyType}
}
