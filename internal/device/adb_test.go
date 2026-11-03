package device

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// NewADB targets one serial with the adb ADB() finds.
func TestNewADB(t *testing.T) {
	fakeADB(t, "")
	a := NewADB("emulator-5554")
	if a.Serial != "emulator-5554" || a.adb != ADB() {
		t.Errorf("NewADB = %+v", a)
	}
}

// Run passes `-s serial` before the arguments and returns stdout.
func TestADBRun(t *testing.T) {
	log := fakeADB(t, `echo "out: $*"`)
	out, err := NewADB("S1").Run(context.Background(), "get-state", "--x")
	if err != nil {
		t.Fatal(err)
	}
	if out != "out: -s S1 get-state --x\n" {
		t.Errorf("out = %q", out)
	}
	if c := calls(t, log); !reflect.DeepEqual(c, []string{"-s S1 get-state --x"}) {
		t.Errorf("calls %q", c)
	}
}

// A failing adb reports the command, the exit and stderr (or stdout when stderr is
// empty), and still returns stdout.
func TestADBRunErrors(t *testing.T) {
	t.Run("stderr", func(t *testing.T) {
		fakeADB(t, "echo partial; echo '  error: device offline  ' 1>&2; exit 1")
		out, err := NewADB("S1").Run(context.Background(), "get-state")
		if err == nil || err.Error() != "adb get-state: exit status 1: error: device offline" {
			t.Errorf("err = %v", err)
		}
		if out != "partial\n" {
			t.Errorf("out = %q", out)
		}
	})
	t.Run("stdout only", func(t *testing.T) {
		fakeADB(t, "echo 'adb: no devices/emulators found'; exit 1")
		_, err := NewADB("S1").Run(context.Background(), "shell", "true")
		if err == nil || err.Error() != "adb shell true: exit status 1: adb: no devices/emulators found" {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		fakeADB(t, "echo never")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := NewADB("S1").Run(ctx, "get-state"); err == nil || !strings.HasPrefix(err.Error(), "adb get-state: ") {
			t.Errorf("err = %v", err)
		}
	})
}

// Shell runs one script through `adb shell`.
func TestADBShell(t *testing.T) {
	log := fakeADB(t, "echo ok")
	out, err := NewADB("S1").Shell(context.Background(), "pm list packages")
	if err != nil || out != "ok\n" {
		t.Errorf("Shell = %q, %v", out, err)
	}
	if c := calls(t, log); !reflect.DeepEqual(c, []string{"-s S1 shell pm list packages"}) {
		t.Errorf("calls %q", c)
	}
}

// Forward asks adb for a free port (tcp:0) and parses the port it prints.
func TestADBForward(t *testing.T) {
	cases := []struct {
		name, body string
		port       int
		err        string
	}{
		{"port", "echo 38123", 38123, ""},
		{"port with spaces", "echo '  4040  '", 4040, ""},
		{"zero", "echo 0", 0, `adb forward gave no port: "0\n"`},
		{"garbage", "echo nope", 0, `adb forward gave no port: "nope\n"`},
		{"empty", "true", 0, `adb forward gave no port: ""`},
		{"adb fails", "echo 'error: closed' 1>&2; exit 1", 0, "adb forward tcp:0 localabstract:tb: exit status 1: error: closed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			log := fakeADB(t, c.body)
			port, err := NewADB("S1").Forward(context.Background(), "localabstract:tb")
			if c.err != "" {
				if err == nil || err.Error() != c.err {
					t.Errorf("err = %v, want %q", err, c.err)
				}
			} else if err != nil || port != c.port {
				t.Errorf("Forward = %d, %v; want %d", port, err, c.port)
			}
			if got := calls(t, log); !reflect.DeepEqual(got, []string{"-s S1 forward tcp:0 localabstract:tb"}) {
				t.Errorf("calls %q", got)
			}
		})
	}
}

// ExistingForward finds this serial's local port for the remote socket in `forward --list`.
func TestADBExistingForward(t *testing.T) {
	list := heredoc(`OTHER tcp:1111 localabstract:tb
S1 tcp:2222 localabstract:other
S1 localabstract:x localabstract:tb
S1 tcp:38123 localabstract:tb
S1 tcp:4444 localabstract:tb extra`)
	log := fakeADB(t, list)
	a := NewADB("S1")
	if p := a.ExistingForward(context.Background(), "localabstract:tb"); p != 38123 {
		t.Errorf("port = %d, want 38123", p)
	}
	if p := a.ExistingForward(context.Background(), "tcp:9999"); p != 0 {
		t.Errorf("unforwarded remote: port = %d", p)
	}
	if p := NewADB("NOPE").ExistingForward(context.Background(), "localabstract:tb"); p != 0 {
		t.Errorf("other serial: port = %d", p)
	}
	// the list is global: no -s
	if c := calls(t, log); len(c) != 3 || c[0] != "forward --list" {
		t.Errorf("calls %q", c)
	}
	t.Run("adb fails", func(t *testing.T) {
		fakeADB(t, list+"\nexit 1")
		if p := NewADB("S1").ExistingForward(context.Background(), "localabstract:tb"); p != 0 {
			t.Errorf("port = %d", p)
		}
	})
}
