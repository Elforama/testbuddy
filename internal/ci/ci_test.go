package ci

import "testing"

func TestOn(t *testing.T) {
	for _, tc := range []struct {
		tbCI, ci string
		want     bool
	}{
		{"-", "-", false},
		{"-", "true", true},
		{"-", "1", true},
		{"-", "false", false},
		{"1", "-", true},
		{"true", "-", true},
		{"0", "true", false},
		{"false", "true", false},
		{"", "true", true},
		{" TRUE ", "-", true},
	} {
		set := func(k, v string) {
			if v == "-" {
				t.Setenv(k, "")
				return
			}
			t.Setenv(k, v)
		}
		set("TB_CI", tc.tbCI)
		set("CI", tc.ci)
		if got := On(); got != tc.want {
			t.Errorf("TB_CI=%q CI=%q: %v, want %v", tc.tbCI, tc.ci, got, tc.want)
		}
	}
}
