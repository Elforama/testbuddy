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

type Owner struct {
	PID     int       `json:"pid"`
	What    string    `json:"what"`
	Dir     string    `json:"dir,omitempty"`
	Since   time.Time `json:"since"`
	Command string    `json:"command,omitempty"`
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

type Busy struct {
	Serial string
	Owner  Owner
}

func (e *Busy) Error() string {
	return fmt.Sprintf("device %s is in use by %s", e.Serial, e.Owner)
}

func IsBusy(err error) (*Busy, bool) {
	var b *Busy
	ok := errors.As(err, &b)
	return b, ok
}

type Lock struct {
	Serial string
	f      *os.File
	once   sync.Once
}

func (l *Lock) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		unlockFile(l.f)
		l.f.Close()
	})
}

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

func Disabled() bool { return os.Getenv("TB_NO_DEVICE_LOCK") == "1" }

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

func Holder(serial string) Owner {
	b, err := os.ReadFile(path(serial))
	if err != nil {
		return Owner{}
	}
	var o Owner
	json.Unmarshal(b, &o)
	return o
}
