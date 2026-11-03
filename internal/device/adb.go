package device

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ADBDevice runs adb commands against one device.
type ADBDevice struct {
	Serial string
	adb    string
}

func NewADB(serial string) *ADBDevice { return &ADBDevice{Serial: serial, adb: ADB()} }

// Run runs `adb -s serial args...` and returns stdout.
func (a *ADBDevice) Run(ctx context.Context, args ...string) (string, error) {
	full := append([]string{"-s", a.Serial}, args...)
	cmd := exec.CommandContext(ctx, a.adb, full...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return out.String(), fmt.Errorf("adb %s: %v: %s", strings.Join(args, " "), err, msg)
	}
	return out.String(), nil
}

// Shell runs a shell command on the device.
func (a *ADBDevice) Shell(ctx context.Context, script string) (string, error) {
	return a.Run(ctx, "shell", script)
}

// Forward forwards a free local TCP port to a device socket ("localabstract:name" or "tcp:N")
// and returns the local port.
func (a *ADBDevice) Forward(ctx context.Context, remote string) (int, error) {
	out, err := a.Run(ctx, "forward", "tcp:0", remote)
	if err != nil {
		return 0, err
	}
	var port int
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &port); err != nil || port == 0 {
		return 0, fmt.Errorf("adb forward gave no port: %q", out)
	}
	return port, nil
}

// ExistingForward returns the local port already forwarded to remote, or 0.
func (a *ADBDevice) ExistingForward(ctx context.Context, remote string) int {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, a.adb, "forward", "--list").Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[0] == a.Serial && f[2] == remote && strings.HasPrefix(f[1], "tcp:") {
			var p int
			fmt.Sscanf(f[1], "tcp:%d", &p)
			return p
		}
	}
	return 0
}
