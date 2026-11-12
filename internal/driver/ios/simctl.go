package ios

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func (d *Driver) simctl(ctx context.Context, args ...string) (string, error) {
	return d.runSimctl(ctx, nil, false, args...)
}

func (d *Driver) simctlEnv(ctx context.Context, env []string, args ...string) (string, error) {
	return d.runSimctl(ctx, env, false, args...)
}

// simctlOwnSession runs simctl in a session of its own, so a Ctrl-C at tb's terminal (sent to
// tb's whole process group) does not stop it: only ctx does.
func (d *Driver) simctlOwnSession(ctx context.Context, args ...string) (string, error) {
	return d.runSimctl(ctx, nil, true, args...)
}

func (d *Driver) runSimctl(ctx context.Context, env []string, ownSess bool, args ...string) (string, error) {
	d.ready.Store(nil)
	cmd := exec.CommandContext(ctx, "xcrun", append([]string{"simctl"}, args...)...)
	if env != nil {
		cmd.Env = env
	}
	if ownSess {
		cmd.SysProcAttr = ownSession()
		// When ctx ends, the whole session goes, and Run returns even if something it started
		// still holds the output open.
		cmd.Cancel = func() error { killSession(cmd.Process.Pid); return nil }
		cmd.WaitDelay = 2 * time.Second
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	s := strings.TrimSpace(out.String())
	if err != nil {
		if s == "" {
			s = err.Error()
		}
		return s, fmt.Errorf("simctl %s: %s", strings.Join(args, " "), s)
	}
	return s, nil
}

func validID(id string) error {
	if id == "" {
		return errors.New("no app id given")
	}
	for _, r := range id {
		if !(r == '.' || r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return fmt.Errorf("invalid bundle id %q", id)
		}
	}
	return nil
}
