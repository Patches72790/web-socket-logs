package sftp

import (
	"fmt"
	"os"

	//"os"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type SftpSession struct {
	*sftp.Client
}

type ServerConfig struct {
	username string
	password string
}

func makeSSHClientConfig(username string) (*ssh.ClientConfig, error) {
	// TODO => Lookup the password based on host from env variables or config somehow
	ssh_password := os.Getenv("SSH_PASSWORD")

	if ssh_password == "" {
		return nil, fmt.Errorf("Error finding SSH Password for SFTP session")
	}

	return &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(ssh_password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}, nil
}

func NewSftpSession(username, hostname string) (*SftpSession, error) {
	clientConfig, err := makeSSHClientConfig(username)

	if err != nil {
		return nil, fmt.Errorf("Error getting ssh client connection: %s", err)
	}

	sshClient, err := ssh.Dial("tcp", hostname+":22", clientConfig)
	if err != nil {
		return nil, fmt.Errorf("Error connecting to ssh session: %s", err)
	}

	sftp, err := sftp.NewClient(sshClient)

	if err != nil {
		return nil, fmt.Errorf("Error creating sftp connection: %s", err)
	}

	return &SftpSession{
		sftp,
	}, nil
}
