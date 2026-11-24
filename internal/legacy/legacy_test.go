package legacy

import (
	"reflect"
	"strings"
	"testing"
)

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

func TestAndroidDriverPackages(t *testing.T) {
	want := []string{"dev.mobile.maestro.test", "dev.mobile.maestro"}
	if got := AndroidDriverPackages(); !reflect.DeepEqual(got, want) {
		t.Errorf("AndroidDriverPackages() = %v, want %v", got, want)
	}
	a := AndroidDriverPackages()
	a[0] = "changed"
	if AndroidDriverPackages()[0] != want[0] {
		t.Error("AndroidDriverPackages shares its backing array")
	}
}

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
