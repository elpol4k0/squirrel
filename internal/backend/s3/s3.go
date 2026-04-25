package s3

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/elpol4k0/squirrel/internal/backend"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type S3 struct {
	client *minio.Client
	bucket string
	prefix string
}

// creds: AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN
func New(endpoint, bucket, prefix string, useSSL bool) (*S3, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewEnvAWS(),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}
	return &S3{client: client, bucket: bucket, prefix: strings.TrimRight(prefix, "/")}, nil
}

// s3:bucket/prefix or s3:endpoint/bucket/prefix (custom endpoint: MinIO, Backblaze B2…)
func ParseURL(rawURL string) (*S3, error) {
	s := strings.TrimPrefix(rawURL, "s3:")
	s = strings.TrimPrefix(s, "//")

	parts := strings.SplitN(s, "/", 3)
	if len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("invalid S3 URL %q", rawURL)
	}

	var endpoint, bucket, prefix string
	useSSL := true

	// If the first segment contains a dot or colon it looks like an endpoint host.
	if strings.ContainsAny(parts[0], ".:") {
		endpoint = parts[0]
		if len(parts) > 1 {
			bucket = parts[1]
		}
		if len(parts) > 2 {
			prefix = parts[2]
		}
	} else {
		endpoint = "s3.amazonaws.com"
		bucket = parts[0]
		if len(parts) > 1 {
			prefix = strings.Join(parts[1:], "/")
		}
	}

	if bucket == "" {
		return nil, fmt.Errorf("S3 URL %q: bucket name is empty", rawURL)
	}
	return New(endpoint, bucket, prefix, useSSL)
}

func (s *S3) key(h backend.Handle) string {
	var path string
	if h.Type == backend.TypeData && len(h.Name) >= 2 {
		path = fmt.Sprintf("%s/%s/%s", h.Type, h.Name[:2], h.Name)
	} else {
		path = fmt.Sprintf("%s/%s", h.Type, h.Name)
	}
	if s.prefix == "" {
		return path
	}
	return s.prefix + "/" + path
}

func (s *S3) Save(ctx context.Context, h backend.Handle, rd io.Reader) error {
	_, err := s.client.PutObject(ctx, s.bucket, s.key(h), rd, -1, minio.PutObjectOptions{
		ContentType: "application/octet-stream",
	})
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", s.key(h), err)
	}
	return nil
}

func (s *S3) Load(ctx context.Context, h backend.Handle) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, s.key(h), minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("s3 get %s: %w", s.key(h), err)
	}
	return obj, nil
}

func (s *S3) List(ctx context.Context, t backend.FileType) ([]string, error) {
	prefix := string(t) + "/"
	if s.prefix != "" {
		prefix = s.prefix + "/" + prefix
	}

	var names []string
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("s3 list: %w", obj.Err)
		}
		// strip prefix and shard subdirectory to get just the name
		name := strings.TrimPrefix(obj.Key, prefix)
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

func (s *S3) Remove(ctx context.Context, h backend.Handle) error {
	return s.client.RemoveObject(ctx, s.bucket, s.key(h), minio.RemoveObjectOptions{})
}

func (s *S3) Stat(ctx context.Context, h backend.Handle) (backend.FileInfo, error) {
	info, err := s.client.StatObject(ctx, s.bucket, s.key(h), minio.StatObjectOptions{})
	if err != nil {
		return backend.FileInfo{}, fmt.Errorf("s3 stat %s: %w", s.key(h), err)
	}
	return backend.FileInfo{Name: h.Name, Size: info.Size}, nil
}

func (s *S3) Exists(ctx context.Context, h backend.Handle) (bool, error) {
	_, err := s.client.StatObject(ctx, s.bucket, s.key(h), minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
