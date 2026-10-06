// Package diagnostic provides opt-in, metadata-only tracing on stderr.
package diagnostic

import (
	"context"
	"fmt"
	"io"
	"sync"
)

type key struct{}
type logger struct {
	mu sync.Mutex
	w  io.Writer
}

func WithWriter(ctx context.Context, w io.Writer) context.Context {
	return context.WithValue(ctx, key{}, &logger{w: w})
}

// Printf accepts only operational metadata, never credentials or payloads.
func Printf(ctx context.Context, format string, args ...any) {
	l, ok := ctx.Value(key{}).(*logger)
	if !ok {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintf(l.w, "debug: "+format+"\n", args...)
}
