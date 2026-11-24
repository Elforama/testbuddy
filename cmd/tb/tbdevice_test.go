package main

import (
	"context"
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

// issue #399: TB_SESSION_OWNER goes with every daemon command as "owner"; tb close --force too.
func TestTBSessionOwnerIsSent(t *testing.T) {
	f := startFakeDaemon(t)
	t.Setenv("TB_SESSION_OWNER", "agent-1")
	agentCall("look", nil, nil)
	if _, args, _ := f.last(t); args["owner"] != "agent-1" {
		t.Errorf("look: %v", args)
	}
	agentCall("close", []string{"all", "--force"}, []flagSpec{{[]string{"force"}, boolFlag}})
	if cmd, args, _ := f.last(t); cmd != "close" || args["owner"] != "agent-1" || args["force"] != true || args["device"] != "all" {
		t.Errorf("close all --force: %s %v", cmd, args)
	}
	t.Setenv("TB_SESSION_OWNER", "")
	agentCall("look", nil, nil)
	if _, args, _ := f.last(t); args["owner"] != nil {
		t.Errorf("no owner: %v", args)
	}
}

// Review of issue #399: the MCP devices tool sends the owner too, so an agent's own sessions are
// not marked as another agent's.
func TestMCPDevicesSendsTheOwner(t *testing.T) {
	f := startFakeDaemon(t)
	t.Setenv("TB_SESSION_OWNER", "agent-1")
	mcpCall(context.Background(), "devices", map[string]any{})
	if cmd, args, _ := f.last(t); cmd != "devices" || args["owner"] != "agent-1" {
		t.Errorf("mcp devices: %s %v", cmd, args)
	}
}

// Review of issue #401: --no-wait reaches the daemon from tb do, tb wait and tb save as well as
// the session commands; tb close takes no --no-wait.
func TestNoWaitIsSent(t *testing.T) {
	f := startFakeDaemon(t)
	for _, c := range []struct {
		cmd string
		run func()
	}{
		{"look", func() { agentCall("look", []string{"-d", "a1", "--no-wait"}, nil) }},
		{"do", func() { cmdDo([]string{"-d", "a1", "--no-wait", "tapOn: Login"}) }},
		{"do", func() { cmdWait([]string{"-d", "a1", "--no-wait", "Login"}) }},
	} {
		c.run()
		if cmd, args, _ := f.last(t); cmd != c.cmd || args["no_wait"] != true {
			t.Errorf("--no-wait, for %s: %s %v", c.cmd, cmd, args)
		}
	}
	if code := agentCall("close", []string{"-d", "a1", "--no-wait"}, nil); code != 2 {
		t.Errorf("tb close --no-wait: exit %d, want 2 (no such flag)", code)
	}
}
