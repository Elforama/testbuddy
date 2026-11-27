package session

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/elforama/testbuddy/internal/daemon"
)

const hiddenText = "•••"

func (d *Dev) keepSecrets(secrets map[string]string) error {
	for name, value := range secrets {
		if name == "" || value == "" {
			return fmt.Errorf("secret %q has no value", name)
		}
	}
	d.stepsMu.Lock()
	defer d.stepsMu.Unlock()
	for name, value := range secrets {
		if d.secrets == nil {
			d.secrets = map[string]string{}
		}
		d.secrets[name] = value
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
	defer d.stepsMu.Unlock()
	steps := strings.Join(d.steps, "\n")
	var vars []string
	for name := range d.secrets {
		if strings.Contains(steps, "${"+name+"}") {
			vars = append(vars, " -e "+name+"=...")
		}
	}
	if len(vars) == 0 {
		return ""
	}
	sort.Strings(vars)
	return "Secret values are not saved. Pass them on replay: tb replay " + path + " -d DEVICE" + strings.Join(vars, "")
}

func replacer(shown map[string]string) *strings.Replacer {
	var pairs [][2]string
	for value, as := range shown {
		for _, spelled := range []string{value, regexp.QuoteMeta(value)} {
			quoted := strconv.Quote(spelled)
			pairs = append(pairs, [2]string{spelled, as}, [2]string{quoted[1 : len(quoted)-1], as})
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

func (s *Service) hider() *strings.Replacer {
	s.mu.Lock()
	devs := make([]*Dev, 0, len(s.devs))
	for _, d := range s.devs {
		devs = append(devs, d)
	}
	s.mu.Unlock()
	shown := map[string]string{}
	for _, d := range devs {
		for value := range d.secretNames() {
			shown[value] = hiddenText
		}
	}
	return replacer(shown)
}

var jsonString = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

func (s *Service) hide(r daemon.Response) daemon.Response {
	h := s.hider()
	if h == nil {
		return r
	}
	r.Text = h.Replace(r.Text)
	if len(r.JSON) > 0 {
		r.JSON = jsonString.ReplaceAllFunc(r.JSON, func(lit []byte) []byte {
			var text string
			if json.Unmarshal(lit, &text) != nil {
				return lit
			}
			out, _ := json.Marshal(h.Replace(text))
			return out
		})
	}
	return r
}

func (s *Service) hiding(h daemon.Handler) daemon.Handler {
	return func(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
		out := send
		if send != nil {
			out = func(r daemon.Response) { send(s.hide(r)) }
		}
		return s.hide(h(ctx, req, out))
	}
}
