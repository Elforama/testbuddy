package orchestrate

import (
	"regexp"
	"strings"

	"github.com/elforama/testbuddy/internal/flow"
)

var varRef = regexp.MustCompile(`\$\{\s*([A-Za-z_$][\w$]*)\s*(?:\|\|\s*(?:'([^']*)'|"([^"]*)"|` + "`([^`]*)`" + `)\s*)?\}`)

type vars map[string]string

func (v vars) define(kvs []flow.KV) vars {
	out := make(vars, len(v)+len(kvs))
	for k, val := range v {
		out[k] = val
	}
	for _, kv := range kvs {
		out[kv.Key] = v.resolve(kv.Value)
	}
	return out
}

func (v vars) resolve(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range varRef.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(s[last:m[0]])
		last = m[1]
		val, ok := v[s[m[2]:m[3]]]
		switch {
		case ok && val != "":
			b.WriteString(val)
		case m[4] >= 0 || m[6] >= 0 || m[8] >= 0:
			for g := 4; g <= 8; g += 2 {
				if m[g] >= 0 {
					b.WriteString(s[m[g]:m[g+1]])
				}
			}
		case ok:
		default:
			b.WriteString(s[m[0]:m[1]])
		}
	}
	b.WriteString(s[last:])
	return b.String()
}
