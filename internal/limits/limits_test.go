package limits

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadFileRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.pdf")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxOutputBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := ReadFile(path); !errors.Is(err, ErrOutputLimit) {
		t.Fatal(err)
	}
}
func TestGateCancelledWait(t *testing.T) {
	gate := NewGate(1)
	gate.Acquire(context.Background())
	defer gate.Release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := gate.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
