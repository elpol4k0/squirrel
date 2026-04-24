package chunker

import (
	"io"

	resticchunker "github.com/restic/chunker"
)

const Polynomial = resticchunker.Pol(0x3DA3358B4DC173)

const (
	MinSize = 512 * 1024
	AvgSize = 1 * 1024 * 1024
	MaxSize = 8 * 1024 * 1024
)

type Chunk struct {
	Data   []byte
	Length uint
	Offset uint64
}

type Splitter struct {
	inner  *resticchunker.Chunker
	offset uint64
	buf    []byte
}

func New(r io.Reader) *Splitter {
	return &Splitter{
		inner: resticchunker.New(r, Polynomial),
		buf:   make([]byte, MaxSize),
	}
}

func (s *Splitter) Next() (Chunk, error) {
	c, err := s.inner.Next(s.buf)
	if err != nil {
		return Chunk{}, err
	}
	// restic/chunker reuses its buffer; copy before returning
	data := make([]byte, c.Length)
	copy(data, c.Data[:c.Length])
	ch := Chunk{Data: data, Length: c.Length, Offset: s.offset}
	s.offset += uint64(c.Length)
	return ch, nil
}

func Split(r io.Reader, fn func(Chunk) error) error {
	s := New(r)
	for {
		ch, err := s.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := fn(ch); err != nil {
			return err
		}
	}
}
