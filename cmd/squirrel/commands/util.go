package commands

import (
	"encoding/json"
	"io"
)

func newJSONEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc
}
