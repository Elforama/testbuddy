package orchestrate

import (
	"regexp"
	"strings"

	"github.com/elforama/testbuddy/internal/flow"
)

// varRef matches the ${...} forms the pool can resolve before a run: ${NAME} and
// ${NAME || 'default'} (the usual way a flow's env gives a default). Anything else is
// left for the engine's JavaScript at run time.
var varRef = regexp.MustCompile(`\$\{\s*([A-Za-z_$][\w$]*)\s*(?:\|\|\s*(?:'([^']*)'|"([^"]*)"|` + "`([^`]*)`" + `)\s*)?\}`)

// vars is a best-effort view of the variables a flow will see, built in the engine's
// order so device choice agrees with the run.
type vars map[string]string

// define sets one env block. Like the engine, every value is resolved against the
// variables before the block, so siblings don't see each other.
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

// resolve substitutes the references it can; an unknown name without a default stays as
// written, so the result still holds ${ and the caller treats it as undecided. As in
// JavaScript, an empty value falls through to the default.
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
		case m[4] >= 0 || m[6] >= 0 || m[8] >= 0: // a || default
			for g := 4; g <= 8; g += 2 {
				if m[g] >= 0 {
					b.WriteString(s[m[g]:m[g+1]])
				}
			}
		case ok:
			// defined but empty, no default
		default:
			b.WriteString(s[m[0]:m[1]])
		}
	}
	b.WriteString(s[last:])
	return b.String()
}
