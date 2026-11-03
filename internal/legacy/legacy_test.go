package legacy

import (
	"reflect"
	"strings"
	"testing"
)

// TestNames pins every Maestro alias tb accepts; other packages build on these exact strings.
func TestNames(t *testing.T) {
	for _, tc := range []struct{ name, got, want string }{
		{"ScriptObject", ScriptObject(), "maestro"},
		{"EnvPrefix", EnvPrefix(), "MAESTRO_"},
		{"FlowsFolder", FlowsFolder(), ".maestro"},
		{"HostProcessPattern", HostProcessPattern(), "maestro"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("%s() = %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}
}

// TestAndroidDriverPackages pins Maestro's on-device driver packages, test package first.
func TestAndroidDriverPackages(t *testing.T) {
	want := []string{"dev.mobile.maestro.test", "dev.mobile.maestro"}
	if got := AndroidDriverPackages(); !reflect.DeepEqual(got, want) {
		t.Errorf("AndroidDriverPackages() = %v, want %v", got, want)
	}
	// Each call returns a fresh slice, so a caller cannot corrupt the list for others.
	a := AndroidDriverPackages()
	a[0] = "changed"
	if AndroidDriverPackages()[0] != want[0] {
		t.Error("AndroidDriverPackages shares its backing array")
	}
}

// TestNamesAreConsistent pins that the aliases derive from one name (an env var built
// from EnvPrefix matches what Maestro sets, the folder is the hidden script object name).
func TestNamesAreConsistent(t *testing.T) {
	if EnvPrefix() != strings.ToUpper(ScriptObject())+"_" {
		t.Errorf("EnvPrefix %q vs ScriptObject %q", EnvPrefix(), ScriptObject())
	}
	if FlowsFolder() != "."+ScriptObject() {
		t.Errorf("FlowsFolder %q", FlowsFolder())
	}
	if EnvPrefix()+"FILENAME" != "MAESTRO_FILENAME" {
		t.Errorf("env var: %q", EnvPrefix()+"FILENAME")
	}
	for _, p := range AndroidDriverPackages() {
		if !strings.Contains(p, HostProcessPattern()) {
			t.Errorf("driver package %q does not contain the process pattern", p)
		}
	}
}
