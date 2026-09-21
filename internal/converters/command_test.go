package converters

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCommandCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sleep", "10")
	start := time.Now()
	err := runCommand(ctx, cmd, t.TempDir())
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("got %v after %s", err, time.Since(start))
	}
}
func TestCommandScratchLimit(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "large.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(65 << 20); err != nil {
		t.Fatal(err)
	}
	f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = runCommand(ctx, exec.CommandContext(ctx, "sleep", "10"), dir)
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("scratch guard: %v", err)
	}
}
