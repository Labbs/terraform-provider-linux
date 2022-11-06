package pkg

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type conn struct {
	Host       string
	Port       int
	User       string
	Password   string
	PrivateKey string
	UseSudo    bool
}

type Client struct {
	connection *ssh.Client
	useSudo    bool
}

func (c *conn) Client() (*Client, error) {
	var auths []ssh.AuthMethod
	var sshAgent net.Conn
	keys := []ssh.Signer{}

	if c.Password != "" {
		auths = append(auths, ssh.Password(c.Password))
	} else {
		key, err := ioutil.ReadFile(c.PrivateKey)
		if err != nil {
			return nil, err
		}

		if sshAgent, err = net.Dial("unix", os.Getenv("SSH_AUTH_SOCK")); err == nil {
			signers, err := agent.NewClient(sshAgent).Signers()
			if err == nil {
				keys = append(keys, signers...)
			}
		}

		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, err
		}

		auths = append(auths, ssh.PublicKeys(signer))
	}

	config := &ssh.ClientConfig{
		User:            c.User,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", c.Host, c.Port), config)
	if err != nil {
		return nil, err
	}

	return &Client{
		connection: client,
		useSudo:    c.UseSudo,
	}, nil
}
