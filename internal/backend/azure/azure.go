package azure

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"

	"github.com/elpol4k0/squirrel/internal/backend"
)

type Azure struct {
	client    *azblob.Client
	container string
	prefix    string
}

// az:container[/prefix] or az:account.blob.core.windows.net/container[/prefix]; creds: AZURE_STORAGE_CONNECTION_STRING or AZURE_STORAGE_ACCOUNT+AZURE_STORAGE_KEY
func ParseURL(rawURL string) (*Azure, error) {
	s := strings.TrimPrefix(rawURL, "az:")
	s = strings.TrimPrefix(s, "//")

	parts := strings.SplitN(s, "/", 2)
	if len(parts) < 1 || parts[0] == "" {
		return nil, fmt.Errorf("invalid Azure URL %q", rawURL)
	}
	container := parts[0]
	prefix := ""
	if len(parts) == 2 {
		prefix = strings.TrimRight(parts[1], "/")
	}

	client, err := newClient()
	if err != nil {
		return nil, err
	}
	return &Azure{client: client, container: container, prefix: prefix}, nil
}

func newClient() (*azblob.Client, error) {
	if cs := os.Getenv("AZURE_STORAGE_CONNECTION_STRING"); cs != "" {
		return azblob.NewClientFromConnectionString(cs, nil)
	}
	account := os.Getenv("AZURE_STORAGE_ACCOUNT")
	key := os.Getenv("AZURE_STORAGE_KEY")
	if account == "" || key == "" {
		return nil, fmt.Errorf("set AZURE_STORAGE_CONNECTION_STRING or AZURE_STORAGE_ACCOUNT + AZURE_STORAGE_KEY")
	}
	cred, err := azblob.NewSharedKeyCredential(account, key)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("https://%s.blob.core.windows.net", account)
	return azblob.NewClientWithSharedKeyCredential(url, cred, nil)
}

func (a *Azure) blobKey(h backend.Handle) string {
	var path string
	if h.Type == backend.TypeData && len(h.Name) >= 2 {
		path = fmt.Sprintf("%s/%s/%s", h.Type, h.Name[:2], h.Name)
	} else {
		path = fmt.Sprintf("%s/%s", h.Type, h.Name)
	}
	if a.prefix == "" {
		return path
	}
	return a.prefix + "/" + path
}

func (a *Azure) Save(ctx context.Context, h backend.Handle, rd io.Reader) error {
	_, err := a.client.UploadStream(ctx, a.container, a.blobKey(h), rd, nil)
	if err != nil {
		return fmt.Errorf("azure upload %s: %w", a.blobKey(h), err)
	}
	return nil
}

func (a *Azure) Load(ctx context.Context, h backend.Handle) (io.ReadCloser, error) {
	resp, err := a.client.DownloadStream(ctx, a.container, a.blobKey(h), nil)
	if err != nil {
		return nil, fmt.Errorf("azure download %s: %w", a.blobKey(h), err)
	}
	return resp.Body, nil
}

func (a *Azure) List(ctx context.Context, t backend.FileType) ([]string, error) {
	prefix := string(t) + "/"
	if a.prefix != "" {
		prefix = a.prefix + "/" + prefix
	}
	pager := a.client.NewListBlobsFlatPager(a.container, &azblob.ListBlobsFlatOptions{
		Prefix: &prefix,
	})
	var names []string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("azure list: %w", err)
		}
		for _, item := range page.Segment.BlobItems {
			name := strings.TrimPrefix(*item.Name, prefix)
			if idx := strings.LastIndex(name, "/"); idx >= 0 {
				name = name[idx+1:]
			}
			if name != "" {
				names = append(names, name)
			}
		}
	}
	return names, nil
}

func (a *Azure) Remove(ctx context.Context, h backend.Handle) error {
	_, err := a.client.DeleteBlob(ctx, a.container, a.blobKey(h), nil)
	return err
}

func (a *Azure) Stat(ctx context.Context, h backend.Handle) (backend.FileInfo, error) {
	bc := a.client.ServiceClient().NewContainerClient(a.container).NewBlobClient(a.blobKey(h))
	resp, err := bc.GetProperties(ctx, nil)
	if err != nil {
		return backend.FileInfo{}, fmt.Errorf("azure stat: %w", err)
	}
	return backend.FileInfo{Name: h.Name, Size: *resp.ContentLength}, nil
}

func (a *Azure) Exists(ctx context.Context, h backend.Handle) (bool, error) {
	bc := a.client.ServiceClient().NewContainerClient(a.container).NewBlobClient(a.blobKey(h))
	_, err := bc.GetProperties(ctx, nil)
	if err != nil {
		if bloberror.HasCode(err, bloberror.BlobNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
