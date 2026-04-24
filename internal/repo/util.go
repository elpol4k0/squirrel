package repo

import (
	"bytes"
	"io"
)

func wrapReader(data []byte) io.Reader { return bytes.NewReader(data) }
