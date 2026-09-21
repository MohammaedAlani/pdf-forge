package converters

import (
	"context"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"time"

	"pdf-forge/internal/limits"
)

// runCommand bounds scratch growth while a PDF tool is running. The polling
// guard is best effort (a tool can overshoot between checks); container memory
// and tmpfs limits remain the final resource boundary.
func runCommand(ctx context.Context, cmd *exec.Cmd, dir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			<-done
			return ctx.Err()
		case <-ticker.C:
			var size int64
			err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				size += info.Size()
				if size > 2*limits.MaxOutputBytes {
					return fmt.Errorf("PDF tool scratch exceeds 64 MiB")
				}
				return nil
			})
			if err != nil {
				_ = cmd.Process.Kill()
				<-done
				return err
			}
		}
	}
}

// diagnosticBuffer keeps stderr bounded without blocking the child process.
type diagnosticBuffer struct{ data []byte }

func (b *diagnosticBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (64 << 10) - len(b.data)
	if len(p) > remaining {
		p = p[:remaining]
	}
	b.data = append(b.data, p...)
	return n, nil
}
func (b *diagnosticBuffer) String() string { return string(b.data) }
