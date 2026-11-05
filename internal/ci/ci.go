// Package ci says whether tb runs in CI, where runners are slower than a developer's machine
// and some waits get more room (each such allowance says so where it is made).
//
// TB_CI decides when set: 1 or true turns CI mode on, 0 or false turns it off. Unset, CI mode
// follows CI=true, which GitHub Actions, GitLab CI, CircleCI and most other services set.
package ci

import (
	"os"
	"strings"
)

// On reports whether tb runs in CI.
func On() bool {
	if v, ok := os.LookupEnv("TB_CI"); ok && strings.TrimSpace(v) != "" {
		return truthy(v)
	}
	return truthy(os.Getenv("CI"))
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
