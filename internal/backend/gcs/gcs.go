package gcs

import (
	"context"
	"fmt"
	"io"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"

	"github.com/elpol4k0/squirrel/internal/backend"
)

type GCS struct {
	client *storage.Client
	bucket string
	prefix string
}

// gs:bucket[/prefix]; creds from GOOGLE_APPLICATION_CREDENTIALS or ADC
func ParseURL(rawURL string) (*GCS, error) {
	s := strings.TrimPrefix(rawURL, "gs:")
	s = strings.TrimPrefix(s, "//")

	parts := strings.SplitN(s, "/", 2)
	if parts[0] == "" {
		return nil, fmt.Errorf("invalid GCS URL %q: bucket name missing", rawURL)
	}
	prefix := ""
	if len(parts) == 2 {
		prefix = strings.TrimRight(parts[1], "/")
	}

	ctx := context.Background()
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcs client: %w", err)
	}
	return &GCS{client: client, bucket: parts[0], prefix: prefix}, nil
}

func (g *GCS) objectName(h backend.Handle) string {
	var path string
	if h.Type == backend.TypeData && len(h.Name) >= 2 {
		path = fmt.Sprintf("%s/%s/%s", h.Type, h.Name[:2], h.Name)
	} else {
		path = fmt.Sprintf("%s/%s", h.Type, h.Name)
	}
	if g.prefix == "" {
		return path
	}
	return g.prefix + "/" + path
}

func (g *GCS) Save(ctx context.Context, h backend.Handle, rd io.Reader) error {
	w := g.client.Bucket(g.bucket).Object(g.objectName(h)).NewWriter(ctx)
	if _, err := io.Copy(w, rd); err != nil {
		w.Close()
		return fmt.Errorf("gcs write %s: %w", g.objectName(h), err)
	}
	return w.Close()
}

func (g *GCS) Load(ctx context.Context, h backend.Handle) (io.ReadCloser, error) {
	r, err := g.client.Bucket(g.bucket).Object(g.objectName(h)).NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcs read %s: %w", g.objectName(h), err)
	}
	return r, nil
}

func (g *GCS) List(ctx context.Context, t backend.FileType) ([]string, error) {
	prefix := string(t) + "/"
	if g.prefix != "" {
		prefix = g.prefix + "/" + prefix
	}
	it := g.client.Bucket(g.bucket).Objects(ctx, &storage.Query{Prefix: prefix})
	var names []string
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("gcs list: %w", err)
		}
		name := strings.TrimPrefix(attrs.Name, prefix)
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

func (g *GCS) Remove(ctx context.Context, h backend.Handle) error {
	return g.client.Bucket(g.bucket).Object(g.objectName(h)).Delete(ctx)
}

func (g *GCS) Stat(ctx context.Context, h backend.Handle) (backend.FileInfo, error) {
	attrs, err := g.client.Bucket(g.bucket).Object(g.objectName(h)).Attrs(ctx)
	if err != nil {
		return backend.FileInfo{}, fmt.Errorf("gcs stat: %w", err)
	}
	return backend.FileInfo{Name: h.Name, Size: attrs.Size}, nil
}

func (g *GCS) Exists(ctx context.Context, h backend.Handle) (bool, error) {
	_, err := g.client.Bucket(g.bucket).Object(g.objectName(h)).Attrs(ctx)
	if err == storage.ErrObjectNotExist {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
