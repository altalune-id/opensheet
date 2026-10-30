//go:build unix

package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const lockPoll = 50 * time.Millisecond

func lockSessionFile(ctx context.Context, path string) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // operator-chosen path (session.path).
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd()) //nolint:gosec // G115: a file descriptor fits an int.
	tick := time.NewTicker(lockPoll)
	defer tick.Stop()
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(fd, syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			_ = f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}
