package compress

import (
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
)

var (
	encMu   sync.Mutex
	encoder *zstd.Encoder
	decoder *zstd.Decoder
)

func init() {
	var err error
	encoder, err = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		panic(fmt.Sprintf("compress: create encoder: %v", err))
	}
	decoder, err = zstd.NewReader(nil)
	if err != nil {
		panic(fmt.Sprintf("compress: create decoder: %v", err))
	}
}

func Compress(data []byte) []byte {
	encMu.Lock()
	out := encoder.EncodeAll(data, make([]byte, 0, len(data)/2))
	encMu.Unlock()
	return out
}

func Decompress(data []byte) ([]byte, error) {
	out, err := decoder.DecodeAll(data, nil)
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}
	return out, nil
}
