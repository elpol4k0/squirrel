package compress

import (
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// encoderPool holds reusable zstd.Encoders; each goroutine gets its own to avoid lock contention.
var encoderPool = sync.Pool{
	New: func() any {
		enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
		if err != nil {
			panic(fmt.Sprintf("compress: create encoder: %v", err))
		}
		return enc
	},
}

var decoder *zstd.Decoder

func init() {
	var err error
	// zstd.Decoder.DecodeAll is goroutine-safe; a single shared instance is enough.
	decoder, err = zstd.NewReader(nil)
	if err != nil {
		panic(fmt.Sprintf("compress: create decoder: %v", err))
	}
}

func Compress(data []byte) []byte {
	enc := encoderPool.Get().(*zstd.Encoder)
	out := enc.EncodeAll(data, make([]byte, 0, len(data)/2))
	encoderPool.Put(enc)
	return out
}

func Decompress(data []byte) ([]byte, error) {
	out, err := decoder.DecodeAll(data, nil)
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}
	return out, nil
}
