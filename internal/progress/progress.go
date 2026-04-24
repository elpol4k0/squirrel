package progress

import (
	"fmt"
	"io"
	"os"

	"github.com/schollz/progressbar/v3"
)

type Bar struct {
	bar   *progressbar.ProgressBar
	total int64
}

// NewBytes creates an indeterminate progress bar for streaming byte operations.
func NewBytes(description string) *Bar {
	bar := progressbar.NewOptions64(-1,
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetDescription(description),
		progressbar.OptionShowBytes(true),
		progressbar.OptionShowCount(),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
		progressbar.OptionOnCompletion(func() { fmt.Fprint(os.Stderr, "\n") }),
	)
	return &Bar{bar: bar}
}

// NewKnown creates a progress bar when total size is known.
func NewKnown(description string, total int64) *Bar {
	bar := progressbar.NewOptions64(total,
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetDescription(description),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
		progressbar.OptionOnCompletion(func() { fmt.Fprint(os.Stderr, "\n") }),
	)
	return &Bar{bar: bar, total: total}
}

func (b *Bar) Add(n int) { b.bar.Add(n) }   //nolint:errcheck
func (b *Bar) Finish()   { b.bar.Finish() } //nolint:errcheck
func (b *Bar) Reader(r io.Reader) io.Reader {
	rd := progressbar.NewReader(r, b.bar)
	return &rd
}
