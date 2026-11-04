// Package devlock gives one tb process at a time a device: an Android device or emulator, or
// an iOS simulator. Two runners on one device replace each other's on-device agent and drive
// the same screen: both fail in confusing ways. So every tb process that drives a device
// (`tb test` for the devices its run uses, the daemon for a `tb open` / `tb do` session) holds
// the device's lock, and a second one is told who has it instead of going ahead.
//
// A lock is an flock(2) (LockFileEx on Windows) on a file per device in a machine-wide folder (~/.testbuddy/locks, or
// TB_LOCK_DIR): the system drops it when the holding process exits, however it exits, so a
// crashed or killed run never leaves a device locked. The file also holds who locked it (the
// command, pid, folder, start time), shown to whoever finds the device taken. Browsers need no
// lock: each run starts its own.
package devlock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Owner says who holds a device.
type Owner struct {
	PID     int       `json:"pid"`
	What    string    `json:"what"` // e.g. "tb test flows/login.yaml", "tb session alice"
	Dir     string    `json:"dir,omitempty"`
	Since   time.Time `json:"since"`
	Command string    `json:"command,omitempty"` // the holder's full command line
}

func (o Owner) String() string {
	s := o.What
	if s == "" {
		s = "another tb"
	}
	s += fmt.Sprintf(" (pid %d", o.PID)
	if !o.Since.IsZero() {
		s += ", since " + o.Since.Format("15:04:05")
	}
	if o.Dir != "" {
		s += ", in " + o.Dir
	}
	return s + ")"
}

// Busy is the error for a device another process holds.
type Busy struct {
	Serial string
	Owner  Owner
}

func (e *Busy) Error() string {
	return fmt.Sprintf("device %s is in use by %s", e.Serial, e.Owner)
}

// IsBusy reports whether err is (or wraps) a Busy.
func IsBusy(err error) (*Busy, bool) {
	var b *Busy
	ok := errors.As(err, &b)
	return b, ok
}

// Lock is a held device lock.
type Lock struct {
	Serial string
	f      *os.File
	once   sync.Once
}

// Release frees the device (also done by the system when the process exits).
func (l *Lock) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		unlockFile(l.f)
		l.f.Close()
	})
}

// Dir is where the lock files are.
func Dir() string {
	if d := os.Getenv("TB_LOCK_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "testbuddy-locks")
	}
	return filepath.Join(home, ".testbuddy", "locks")
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func path(serial string) string {
	return filepath.Join(Dir(), unsafeChars.ReplaceAllString(serial, "_")+".lock")
}

// Disabled: TB_NO_DEVICE_LOCK=1 turns locking off (every lock is granted, holding nothing).
func Disabled() bool { return os.Getenv("TB_NO_DEVICE_LOCK") == "1" }

// TryAcquire takes the device's lock at once, or returns a *Busy naming who holds it.
func TryAcquire(serial, what string) (*Lock, error) {
	if Disabled() {
		return nil, nil
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path(serial), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if held, err := lockFile(f); err != nil || held {
		f.Close()
		if held {
			return nil, &Busy{Serial: serial, Owner: Holder(serial)}
		}
		return nil, err
	}
	me := Owner{PID: os.Getpid(), What: what, Since: time.Now(), Command: strings.Join(os.Args, " ")}
	if wd, err := os.Getwd(); err == nil {
		me.Dir = wd
	}
	b, _ := json.Marshal(me)
	f.Truncate(0)
	f.WriteAt(b, 0)
	f.Sync()
	return &Lock{Serial: serial, f: f}, nil
}

// Acquire takes the device's lock, waiting up to wait for its holder to let go (0: not at all).
// It checks again every poll: a lock held by another process sends no event when it goes.
func Acquire(serial, what string, wait time.Duration) (*Lock, error) {
	const poll = 500 * time.Millisecond
	deadline := time.Now().Add(wait)
	for {
		l, err := TryAcquire(serial, what)
		if _, busy := IsBusy(err); !busy || !time.Now().Before(deadline) {
			return l, err
		}
		time.Sleep(min(poll, time.Until(deadline)))
	}
}

// Holder reads who holds (or last held) a device's lock. Only meaningful when TryAcquire
// just failed: a released lock's file keeps its last owner.
func Holder(serial string) Owner {
	b, err := os.ReadFile(path(serial))
	if err != nil {
		return Owner{}
	}
	var o Owner
	json.Unmarshal(b, &o)
	return o
}
