package ci

import (
	"os"
	"strings"
)

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
