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
	"context"
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
	held, err := lockFile(f)
	if held && err == nil {
		held, err = lockFile(f) // once more at once: a probe may have just let go of it
	}
	for i := 0; held && err == nil && i < probeRetries && probing(serial); i++ {
		// a tb devices probing the lock holds it shared for an instant (Probe, which marks
		// it): try again before telling the caller someone has it. A lock really held is
		// not waited for.
		time.Sleep(probeWait)
		held, err = lockFile(f)
	}
	if err != nil || held {
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
	os.Remove(probeMark(serial)) // a mark left by a probe that was killed: no probe holds it now
	b, _ := json.Marshal(me)
	f.Truncate(0)
	f.WriteAt(b, 0)
	f.Sync()
	return &Lock{Serial: serial, f: f}, nil
}

// Acquire takes the device's lock, waiting up to wait for its holder to let go (0: not at all).
// It checks again every poll: a lock held by another process sends no event when it goes.
// A cancelled ctx ends the wait at once with ctx's error, holding no lock (issue #115).
// tb itself does not call it: `tb test --wait-for-device` waits in orchestrate's Pool.
func Acquire(ctx context.Context, serial, what string, wait time.Duration) (*Lock, error) {
	const poll = 500 * time.Millisecond
	deadline := time.Now().Add(wait)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		l, err := TryAcquire(serial, what)
		if _, busy := IsBusy(err); !busy || !time.Now().Before(deadline) {
			return l, err
		}
		t := time.NewTimer(min(poll, time.Until(deadline)))
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

// probeRetries and probeWait: how often, and after how long, TryAcquire tries again on a lock
// found taken while a Probe marks it (probeMark), so that a probe holding it for an instant
// does not get a tb refused.
const (
	probeRetries = 3
	probeWait    = 15 * time.Millisecond
)

// State is what Probe found for a device's lock.
type State int

const (
	Unknown State = iota // locking is off, or the lock could not be read
	Free                 // no tb holds the device
	Held                 // another open lock file holds it: another tb process, or a session
)

// Probe reports, without taking the device, whether a tb process holds it, and who (issue #398:
// tb devices shows it). It checks with a shared lock held for an instant; a TryAcquire at that
// instant tries again (probeRetries) rather than be refused. Locking off (TB_NO_DEVICE_LOCK), or
// a lock file it cannot open or lock: Unknown.
func Probe(serial string) (Owner, State) {
	if Disabled() {
		return Owner{}, Unknown
	}
	f, err := os.Open(path(serial))
	if errors.Is(err, os.ErrNotExist) {
		return Owner{}, Free // never locked
	}
	if os.WriteFile(probeMark(serial), nil, 0o644) == nil {
		defer os.Remove(probeMark(serial))
	}
	if err != nil {
		return Owner{}, Unknown
	}
	defer f.Close()
	held, err := heldElsewhere(f)
	if err != nil {
		return Owner{}, Unknown
	}
	if !held {
		return Owner{}, Free
	}
	o := Holder(serial)
	if o.PID == 0 { // taken a moment ago, its owner not written yet
		time.Sleep(probeWait)
		o = Holder(serial)
	}
	return o, Held
}

// probeMark is the file a Probe keeps next to a device's lock while it probes it.
func probeMark(serial string) string { return path(serial) + ".probe" }

// probing: a Probe may be holding serial's lock for an instant.
func probing(serial string) bool {
	_, err := os.Stat(probeMark(serial))
	return err == nil
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
