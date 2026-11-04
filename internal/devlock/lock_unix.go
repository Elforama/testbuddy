//go:build !windows

package devlock

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes an exclusive lock on f without waiting. held is true when another open file
// (another process, or another TryAcquire in this one) has it.
func lockFile(f *os.File) (held bool, err error) {
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return true, nil
	}
	return false, err
}

func unlockFile(f *os.File) { syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
