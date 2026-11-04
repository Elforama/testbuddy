//go:build windows

package devlock

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileFailImmediately = 0x1
	lockfileExclusiveLock   = 0x2
	errorLockViolation      = syscall.Errno(33)
)

// Windows locks are mandatory: a locked byte range cannot be read by anyone else. So the
// lock is on one byte far past the owner record, and Holder can still read who holds it.
func lockRange() *syscall.Overlapped { return &syscall.Overlapped{OffsetHigh: 0x7fffffff} }

// lockFile takes an exclusive lock on f without waiting. held is true when another open file
// (another process, or another TryAcquire in this one) has it.
func lockFile(f *os.File) (held bool, err error) {
	r, _, e := procLockFileEx.Call(f.Fd(), lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0,
		uintptr(unsafe.Pointer(lockRange())))
	if r != 0 {
		return false, nil
	}
	if errors.Is(e, errorLockViolation) {
		return true, nil
	}
	return false, e
}

func unlockFile(f *os.File) {
	procUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(lockRange())))
}
