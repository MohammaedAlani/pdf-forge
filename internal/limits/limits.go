// Package limits contains shared resource bounds for conversion jobs.
package limits

import (
	"context"
	"errors"
	"io"
	"os"
	"time"
)

const (
	MaxOutputBytes    = 32 << 20
	MaxBatchItems     = 32
	MaxPages          = 100
	MaxDPI            = 300
	ProcessingTimeout = 90 * time.Second
)

var ErrOutputLimit = errors.New("output exceeds 32 MiB limit")

// Gate limits active work. Waiters must have a deadline or cancellation source.
type Gate chan struct{}

func NewGate(n int) Gate {
	if n < 1 {
		n = 1
	}
	return make(Gate, n)
}
func (g Gate) Acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case g <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (g Gate) Release() { <-g }

// ReadFile checks the size before allocating and bounds reads if a file grows.
func ReadFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() > MaxOutputBytes {
		return nil, ErrOutputLimit
	}
	return Read(f, MaxOutputBytes)
}
func Read(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if int64(len(data)) > max {
		return nil, ErrOutputLimit
	}
	return data, err
}

// Buffer stops library output from growing without bound.
type Buffer struct{ Data []byte }

func (b *Buffer) Write(p []byte) (int, error) {
	if len(p) > MaxOutputBytes-len(b.Data) {
		return 0, ErrOutputLimit
	}
	b.Data = append(b.Data, p...)
	return len(p), nil
}
func (b *Buffer) Bytes() []byte { return b.Data }
