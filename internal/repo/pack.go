package repo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/elpol4k0/squirrel/internal/backend"
	"github.com/elpol4k0/squirrel/internal/compress"
	"github.com/elpol4k0/squirrel/internal/crypto"
)

type BlobType uint8

const (
	BlobData BlobType = 0
	BlobTree BlobType = 1
)

// BlobID is SHA-256 of the plaintext. Content-addressed → same data, same ID → dedup.
type BlobID [32]byte

func (b BlobID) String() string { return hex.EncodeToString(b[:]) }

func ParseBlobID(s string) (BlobID, error) {
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != 32 {
		return BlobID{}, fmt.Errorf("invalid blob ID %q", s)
	}
	var id BlobID
	copy(id[:], raw)
	return id, nil
}

func computeID(data []byte) BlobID { return sha256.Sum256(data) }

// packfile layout: [blob_0] [blob_1] … [encrypted_header] [header_len: 4B LE]
// each region: Seal(masterKey, payload) = nonce(12) || ciphertext || tag(16)

type blobEntry struct {
	ID        string   `json:"id"`
	Type      BlobType `json:"type"`
	Offset    int      `json:"offset"`
	Length    int      `json:"length"`
	RawLength int      `json:"raw_length"`
}

type packHeader struct {
	Blobs []blobEntry `json:"blobs"`
}

type PackBlobLocation struct {
	BlobID BlobID
	PackID string
	Offset int
	Length int
}

type Packer struct {
	masterKey crypto.MasterKey
	buf       bytes.Buffer
	blobs     []blobEntry
}

func NewPacker(masterKey crypto.MasterKey) *Packer {
	return &Packer{masterKey: masterKey}
}

func (p *Packer) Size() int { return p.buf.Len() }
func (p *Packer) Len() int  { return len(p.blobs) }

func (p *Packer) Add(blobType BlobType, plaintext []byte) (BlobID, error) {
	id := computeID(plaintext)
	enc, err := crypto.Seal(p.masterKey, compress.Compress(plaintext))
	if err != nil {
		return BlobID{}, fmt.Errorf("seal blob: %w", err)
	}
	p.AddEncrypted(blobType, id, enc, len(plaintext))
	return id, nil
}

// AddEncrypted appends an already-compressed-and-sealed blob to the pack.
// The caller must ensure enc was produced from data of rawLen bytes.
func (p *Packer) AddEncrypted(blobType BlobType, id BlobID, enc []byte, rawLen int) {
	offset := p.buf.Len()
	p.buf.Write(enc)
	p.blobs = append(p.blobs, blobEntry{
		ID:        id.String(),
		Type:      blobType,
		Offset:    offset,
		Length:    len(enc),
		RawLength: rawLen,
	})
}

// Finalize seals the pack and returns its ID, a copy of the raw bytes, and blob locations.
// The caller is responsible for uploading the bytes to the backend.
func (p *Packer) Finalize() (packID string, data []byte, locs []PackBlobLocation, err error) {
	if len(p.blobs) == 0 {
		return "", nil, nil, nil
	}
	hdrJSON, err := json.Marshal(packHeader{Blobs: p.blobs})
	if err != nil {
		return "", nil, nil, fmt.Errorf("marshal pack header: %w", err)
	}
	encHdr, err := crypto.Seal(p.masterKey, hdrJSON)
	if err != nil {
		return "", nil, nil, fmt.Errorf("seal pack header: %w", err)
	}
	p.buf.Write(encHdr)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(encHdr)))
	p.buf.Write(lenBuf[:])

	h := sha256.Sum256(p.buf.Bytes())
	packID = hex.EncodeToString(h[:])
	data = make([]byte, p.buf.Len())
	copy(data, p.buf.Bytes())

	locs = make([]PackBlobLocation, len(p.blobs))
	for i, blob := range p.blobs {
		id, _ := ParseBlobID(blob.ID)
		locs[i] = PackBlobLocation{BlobID: id, PackID: packID, Offset: blob.Offset, Length: blob.Length}
	}
	return packID, data, locs, nil
}

func (p *Packer) Flush(ctx context.Context, b backend.Backend) (string, []PackBlobLocation, error) {
	packID, data, locs, err := p.Finalize()
	if err != nil || packID == "" {
		return packID, locs, err
	}
	if err := b.Save(ctx, backend.Handle{Type: backend.TypeData, Name: packID}, bytes.NewReader(data)); err != nil {
		return "", nil, fmt.Errorf("save packfile: %w", err)
	}
	return packID, locs, nil
}
