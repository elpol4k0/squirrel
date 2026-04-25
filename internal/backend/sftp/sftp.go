package sftp

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/pkg/sftp"

	"github.com/elpol4k0/squirrel/internal/backend"
)

type SFTP struct {
	client *sftp.Client
	root   string
}

// sftp://user@host[:port]/path[/prefix]; auth via SSH_AUTH_SOCK or ~/.ssh/id_* keys
func ParseURL(rawURL string) (*SFTP, error) {
	s := strings.TrimPrefix(rawURL, "sftp://")

	// split user@host:port/path
	var userHost, rootPath string
	if slash := strings.Index(s, "/"); slash >= 0 {
		userHost = s[:slash]
		rootPath = s[slash:]
	} else {
		userHost = s
		rootPath = "/"
	}

	var user, hostPort string
	if at := strings.LastIndex(userHost, "@"); at >= 0 {
		user = userHost[:at]
		hostPort = userHost[at+1:]
	} else {
		user = os.Getenv("USER")
		if user == "" {
			user = os.Getenv("USERNAME")
		}
		hostPort = userHost
	}

	if !strings.Contains(hostPort, ":") {
		hostPort += ":22"
	}

	auths, err := buildAuthMethods()
	if err != nil {
		return nil, err
	}

	host, _, _ := net.SplitHostPort(hostPort)
	hostKeyCallback, err := buildHostKeyCallback(host)
	if err != nil {
		hostKeyCallback = gossh.InsecureIgnoreHostKey() //nolint:gosec
	}

	sshCfg := &gossh.ClientConfig{
		User:            user,
		Auth:            auths,
		HostKeyCallback: hostKeyCallback,
	}

	sshConn, err := gossh.Dial("tcp", hostPort, sshCfg)
	if err != nil {
		return nil, fmt.Errorf("ssh connect %s: %w", hostPort, err)
	}

	c, err := sftp.NewClient(sshConn)
	if err != nil {
		sshConn.Close()
		return nil, fmt.Errorf("sftp client: %w", err)
	}
	return &SFTP{client: c, root: rootPath}, nil
}

func (s *SFTP) filePath(h backend.Handle) string {
	var p string
	if h.Type == backend.TypeData && len(h.Name) >= 2 {
		p = path.Join(string(h.Type), h.Name[:2], h.Name)
	} else {
		p = path.Join(string(h.Type), h.Name)
	}
	return path.Join(s.root, p)
}

func (s *SFTP) Save(_ context.Context, h backend.Handle, rd io.Reader) error {
	fp := s.filePath(h)
	if err := s.client.MkdirAll(path.Dir(fp)); err != nil {
		return fmt.Errorf("sftp mkdir: %w", err)
	}
	tmp := fp + ".tmp"
	f, err := s.client.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("sftp open %s: %w", tmp, err)
	}
	if _, err := io.Copy(f, rd); err != nil {
		f.Close()
		s.client.Remove(tmp)
		return fmt.Errorf("sftp write: %w", err)
	}
	f.Close()
	return s.client.Rename(tmp, fp)
}

func (s *SFTP) Load(_ context.Context, h backend.Handle) (io.ReadCloser, error) {
	f, err := s.client.Open(s.filePath(h))
	if err != nil {
		return nil, fmt.Errorf("sftp open %s: %w", s.filePath(h), err)
	}
	return f, nil
}

func (s *SFTP) List(_ context.Context, t backend.FileType) ([]string, error) {
	dir := path.Join(s.root, string(t))
	if t == backend.TypeData {
		return s.listSharded(dir)
	}
	entries, err := s.client.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

func (s *SFTP) listSharded(dir string) ([]string, error) {
	shards, err := s.client.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, shard := range shards {
		if !shard.IsDir() {
			continue
		}
		entries, err := s.client.ReadDir(path.Join(dir, shard.Name()))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				names = append(names, e.Name())
			}
		}
	}
	return names, nil
}

func (s *SFTP) Remove(_ context.Context, h backend.Handle) error {
	return s.client.Remove(s.filePath(h))
}

func (s *SFTP) Stat(_ context.Context, h backend.Handle) (backend.FileInfo, error) {
	fi, err := s.client.Stat(s.filePath(h))
	if err != nil {
		return backend.FileInfo{}, err
	}
	return backend.FileInfo{Name: fi.Name(), Size: fi.Size()}, nil
}

func (s *SFTP) Exists(_ context.Context, h backend.Handle) (bool, error) {
	_, err := s.client.Stat(s.filePath(h))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func buildAuthMethods() ([]gossh.AuthMethod, error) {
	var methods []gossh.AuthMethod

	// SSH agent
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			methods = append(methods, gossh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}

	// Key files
	home, _ := os.UserHomeDir()
	for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
		keyPath := path.Join(home, ".ssh", name)
		data, err := os.ReadFile(keyPath)
		if err != nil {
			continue
		}
		signer, err := gossh.ParsePrivateKey(data)
		if err != nil {
			continue
		}
		methods = append(methods, gossh.PublicKeys(signer))
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("no SSH authentication methods available; set SSH_AUTH_SOCK or add keys to ~/.ssh/")
	}
	return methods, nil
}

func buildHostKeyCallback(host string) (gossh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	knownHostsFile := path.Join(home, ".ssh", "known_hosts")
	return knownhosts.New(knownHostsFile)
}
