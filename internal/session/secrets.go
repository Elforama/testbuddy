package session

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/elforama/testbuddy/internal/daemon"
)

const (
	hiddenText   = "•••"
	cutMark      = "…"
	minSecret    = 6
	minCutPrefix = 4
)

func (d *Dev) keepSecrets(secrets map[string]string) error {
	for name, value := range secrets {
		if utf8.RuneCountInString(value) < minSecret {
			return fmt.Errorf("secret %s is too short to hide: a secret needs at least %d characters, so that hiding it cannot blank unrelated text", name, minSecret)
		}
	}
	d.stepsMu.Lock()
	defer d.stepsMu.Unlock()
	if d.secrets == nil && len(secrets) > 0 {
		d.secrets, d.hidden = map[string]string{}, map[string]bool{}
	}
	for name, value := range secrets {
		d.secrets[name] = value
		d.hidden[value] = true
	}
	return nil
}

func (d *Dev) secretNames() map[string]string {
	d.stepsMu.Lock()
	defer d.stepsMu.Unlock()
	shown := map[string]string{}
	for name, value := range d.secrets {
		shown[value] = "${" + name + "}"
	}
	return shown
}

func (d *Dev) replayNeeds(path string) string {
	d.stepsMu.Lock()
	steps := strings.Join(d.steps, "\n")
	d.stepsMu.Unlock()
	vars := d.secretVars(steps)
	if len(vars) == 0 {
		return ""
	}
	return "Secret values are not saved. Pass them on replay: tb replay " + path + " -d DEVICE" + strings.Join(vars, "")
}

func (d *Dev) secretVars(steps string) []string {
	d.stepsMu.Lock()
	defer d.stepsMu.Unlock()
	var vars []string
	for name := range d.secrets {
		if strings.Contains(steps, "${"+name+"}") {
			vars = append(vars, " -e "+name+"=...")
		}
	}
	sort.Strings(vars)
	return vars
}

func spellings(value string) []string {
	var out []string
	for _, spaced := range []string{value, strings.Join(strings.Fields(value), " ")} {
		for _, spelled := range []string{spaced, regexp.QuoteMeta(spaced)} {
			quoted := strconv.Quote(spelled)
			out = append(out, spelled, quoted[1:len(quoted)-1])
		}
	}
	return out
}

func replacer(shown map[string]string) *strings.Replacer {
	var pairs [][2]string
	for value, as := range shown {
		for _, spelled := range spellings(value) {
			pairs = append(pairs, [2]string{spelled, as})
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	sort.Slice(pairs, func(i, j int) bool {
		if len(pairs[i][0]) != len(pairs[j][0]) {
			return len(pairs[i][0]) > len(pairs[j][0])
		}
		return pairs[i][0]+pairs[i][1] < pairs[j][0]+pairs[j][1]
	})
	var flat []string
	for _, p := range pairs {
		flat = append(flat, p[0], p[1])
	}
	return strings.NewReplacer(flat...)
}

func (d *Dev) hider() func(string) string {
	d.stepsMu.Lock()
	shown := map[string]string{}
	var spelled []string
	for value := range d.hidden {
		shown[value] = hiddenText
		spelled = append(spelled, spellings(value)...)
	}
	d.stepsMu.Unlock()
	whole := replacer(shown)
	if whole == nil {
		return nil
	}
	return func(text string) string {
		parts := strings.Split(whole.Replace(text), cutMark)
		for i := range parts[:len(parts)-1] {
			parts[i] = hideCutSecret(parts[i], spelled)
		}
		return strings.Join(parts, cutMark)
	}
}

func hideCutSecret(text string, spelled []string) string {
	longest := 0
	for _, s := range spelled {
		runes := []rune(s)
		for n := len(runes) - 1; n >= minCutPrefix; n-- {
			if start := string(runes[:n]); strings.HasSuffix(text, start) {
				longest = max(longest, len(start))
				break
			}
		}
	}
	if longest == 0 {
		return text
	}
	return text[:len(text)-longest] + hiddenText
}

var jsonString = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

func (d *Dev) hide(r daemon.Response) daemon.Response {
	hidden := d.hider()
	if hidden == nil {
		return r
	}
	r.Text = hidden(r.Text)
	if len(r.JSON) > 0 {
		r.JSON = jsonString.ReplaceAllFunc(r.JSON, func(lit []byte) []byte {
			var text string
			if json.Unmarshal(lit, &text) != nil {
				return lit
			}
			out, _ := json.Marshal(hidden(text))
			return out
		})
	}
	return r
}

type askedDev struct{}

func (s *Service) resolve(ctx context.Context, a devArgs) (*Dev, error) {
	d, err := s.resolveDev(ctx, a)
	if asked, ok := ctx.Value(askedDev{}).(*atomic.Pointer[Dev]); ok && d != nil {
		asked.Store(d)
	}
	return d, err
}

func (s *Service) hiding(h daemon.Handler) daemon.Handler {
	return func(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
		var asked atomic.Pointer[Dev]
		hide := func(r daemon.Response) daemon.Response {
			if d := asked.Load(); d != nil {
				return d.hide(r)
			}
			return r
		}
		out := send
		if send != nil {
			out = func(r daemon.Response) { send(hide(r)) }
		}
		return hide(h(context.WithValue(ctx, askedDev{}, &asked), req, out))
	}
}
