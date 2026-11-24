package main

import (
	"testing"
)

// issue #396: TB_DEVICE is the default device of the session commands, so whoever starts an agent
// pins it to its device once; -d still wins.
func TestTBDeviceIsTheDefaultDevice(t *testing.T) {
	f := startFakeDaemon(t)
	t.Setenv("TB_DEVICE", "emulator-5750")
	for _, c := range []struct {
		argv []string
		want string
	}{
		{nil, "emulator-5750"},
		{[]string{"-d", "emulator-5594"}, "emulator-5594"},
	} {
		agentCall("look", c.argv, nil)
		if cmd, args, _ := f.last(t); cmd != "look" || args["device"] != c.want {
			t.Errorf("tb look %v: %s %v, want device %q", c.argv, cmd, args, c.want)
		}
	}
	t.Setenv("TB_DEVICE", "")
	agentCall("look", nil, nil)
	if _, args, _ := f.last(t); args["device"] != "" {
		t.Errorf("no TB_DEVICE: device %v", args["device"])
	}
}

// Review of issue #396: a command with a platform (-p web) asks for that platform's device, not
// the one TB_DEVICE pins.
func TestTBDeviceIsNotUsedWithAPlatform(t *testing.T) {
	f := startFakeDaemon(t)
	t.Setenv("TB_DEVICE", "emulator-5554")
	agentCall("look", []string{"-p", "web"}, nil)
	if _, args, _ := f.last(t); args["device"] != "" || args["platform"] != "web" {
		t.Errorf("tb look -p web with TB_DEVICE: %v", args)
	}
}
