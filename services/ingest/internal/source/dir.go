package source

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// Dir reads .eml files from a folder. Used for tests and the offline demo, and handy
// for importing mail exported from any client.
type Dir struct {
	Path    string
	Account string
}

func (d *Dir) Fetch(ctx context.Context, since time.Time, prefilter func(string, string) bool, fn func(Message) error) error {
	files, err := filepath.Glob(filepath.Join(d.Path, "*.eml"))
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		fh, err := os.Open(f)
		if err != nil {
			return err
		}
		m, err := Parse(fh, d.Account)
		fh.Close()
		if err != nil || m.Date.Before(since) {
			continue
		}
		if prefilter != nil && !prefilter(m.FromAddr, m.Subject) {
			continue
		}
		if err := fn(m); err != nil {
			return err
		}
	}
	return nil
}
