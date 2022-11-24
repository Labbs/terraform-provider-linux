package client

import (
	"fmt"
	"io/ioutil"
	"log"
	"strings"
)

func Command(client *Client, sudo bool, command, content string) (string, string, error) {
	if sudo && client.useSudo {
		command = fmt.Sprintf("sudo %s", command)
	}
	session, err := client.connection.NewSession()
	if err != nil {
		return "", "", fmt.Errorf("failed to create session: %s", err)
	}
	stdin, err := session.StdinPipe()
	defer stdin.Close()
	if err != nil {
		return "", "", fmt.Errorf("failed to create stdin pipe: %s", err)
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		return "", "", fmt.Errorf("failed to create stderr pipe: %s", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return "", "", fmt.Errorf("failed to create stdout pipe: %s", err)
	}

	log.Printf("Running command %s", command)

	var stdoutOutput, stderrOutput []byte
	err = session.Start(command)
	if err != nil {
		return "", "", fmt.Errorf("failed to start session: %s", err)
	}
	if content != "" {
		stdin.Write([]byte(content))
		stdin.Close()
	}
	err = session.Wait()

	if err != nil {
		stderrOutput, err2 := ioutil.ReadAll(stderr)
		if err2 != nil {
			log.Printf("Unable to read stderr for command: %v", err)
		}
		log.Printf("Stderr output: %s", strings.TrimSpace(string(stderrOutput)))

		return string(stdoutOutput), string(stderrOutput), fmt.Errorf("failed to run command: %s, %s", err, command)
	}
	stdoutOutput, err = ioutil.ReadAll(stdout)
	if err != nil {
		return string(stdoutOutput), string(stderrOutput), fmt.Errorf("failed to read stdout: %s, %s", err, command)
	}

	return string(stdoutOutput), string(stderrOutput), nil
}
