package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/elpol4k0/squirrel/internal/backend"
	"github.com/elpol4k0/squirrel/internal/crypto"
)

type Snapshot struct {
	ID       string            `json:"id"`
	Time     time.Time         `json:"time"`
	Hostname string            `json:"hostname"`
	Username string            `json:"username"`
	Paths    []string          `json:"paths"`
	Tree     string            `json:"tree"`
	Tags     []string          `json:"tags,omitempty"`
	Meta     map[string]string `json:"meta,omitempty"` // e.g. start_lsn, end_lsn, timeline, system_id
}

type Tree struct {
	Nodes []TreeNode `json:"nodes"`
}

type TreeNode struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"` // "file" | "dir"
	Size    int64    `json:"size"`
	Mode    uint32   `json:"mode,omitempty"`
	Content []string `json:"content,omitempty"` // file: ordered blob IDs
	Subtree string   `json:"subtree,omitempty"` // dir: tree blob ID
}

func NewSnapshot(paths []string, tags []string) (*Snapshot, error) {
	hostname, _ := os.Hostname()
	username := os.Getenv("USERNAME") // Windows; falls back to USER on Unix
	if username == "" {
		username = os.Getenv("USER")
	}
	return &Snapshot{
		Time:     time.Now().UTC(),
		Hostname: hostname,
		Username: username,
		Paths:    paths,
		Tags:     tags,
	}, nil
}

func (s *Snapshot) Save(ctx context.Context, b backend.Backend, masterKey crypto.MasterKey) error {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	enc, err := crypto.Seal(masterKey, data)
	if err != nil {
		return fmt.Errorf("seal snapshot: %w", err)
	}
	id := randomHex(16)
	s.ID = id
	return b.Save(ctx, backend.Handle{Type: backend.TypeSnapshot, Name: id}, bytes.NewReader(enc))
}
