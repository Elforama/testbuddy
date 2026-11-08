package ios

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func (d *Driver) simctl(ctx context.Context, args ...string) (string, error) {
	return d.simctlEnv(ctx, nil, args...)
}

func (d *Driver) simctlEnv(ctx context.Context, env []string, args ...string) (string, error) {
	d.ready.Store(nil)
	cmd := exec.CommandContext(ctx, "xcrun", append([]string{"simctl"}, args...)...)
	if env != nil {
		cmd.Env = env
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
