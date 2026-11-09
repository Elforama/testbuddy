package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
)

// mockNetwork runs a mockNetwork step: the mock's values are interpolated and passed to a
// driver that can mock requests (the web driver).
func (r *Runner) mockNetwork(ctx context.Context, c *flow.Command) error {
	nm, ok := r.Drv.(driver.NetworkMocker)
	if !ok {
		return failf("mockNetwork works only in web flows (url:)")
	}
	m := c.Mock
	url, err := r.eval(m.URL)
	if err != nil {
		return err
	}
	method, err := r.eval(m.Method)
	if err != nil {
		return err
	}
	out := driver.NetworkMock{URL: url, Method: strings.ToUpper(method), Status: m.Status,
		Delay: time.Duration(m.Delay) * time.Millisecond, Fail: m.Fail}
	if m.Fail {
		return nm.MockNetwork(ctx, out)
	}
	body := m.Body
	if !m.BodyFromFile {
		if body, err = r.eval(body); err != nil {
			return err
		}
	}
	out.Body = []byte(body)
	typed := false
	for _, h := range m.Headers {
		v, err := r.eval(h.Value)
		if err != nil {
			return err
		}
		out.Headers = append(out.Headers, [2]string{h.Key, v})
		typed = typed || strings.EqualFold(h.Key, "Content-Type")
	}
	if !typed {
		out.Headers = append(out.Headers, [2]string{"Content-Type", guessContentType(body, m.BodyFile)})
	}
	return nm.MockNetwork(ctx, out)
}

// guessContentType is the Content-Type of a mock body that names none: JSON for a .json
// file or a body that parses as JSON, else plain text.
func guessContentType(body, file string) string {
	if strings.EqualFold(filepath.Ext(file), ".json") {
		return "application/json"
	}
	if t := strings.TrimSpace(body); t != "" && (t[0] == '{' || t[0] == '[') && json.Valid([]byte(t)) {
		return "application/json"
	}
	return "text/plain; charset=utf-8"
}

func (r *Runner) clearNetworkMocks(ctx context.Context) error {
	nm, ok := r.Drv.(driver.NetworkMocker)
	if !ok {
		return failf("clearNetworkMocks works only in web flows (url:)")
	}
	return nm.ClearNetworkMocks(ctx)
}

// resetNetworkMocks drops mocks left on the device, so a flow starts and ends without any
// (an agent session's commands keep theirs until it clears them).
func (r *Runner) resetNetworkMocks() {
	if nm, ok := r.Drv.(driver.NetworkMocker); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := nm.ClearNetworkMocks(ctx); err != nil {
			r.logLine("clearing network mocks: " + err.Error())
		}
	}
}

func describeMock(m *flow.NetworkMock) string {
	if m == nil {
		return "Mock network"
	}
	what := m.URL
	if m.Method != "" {
		what = m.Method + " " + what
	}
	if m.Fail {
		return fmt.Sprintf("Mock %s → fail", what)
	}
	s := fmt.Sprintf("Mock %s → %d", what, m.Status)
	if m.Delay > 0 {
		s += fmt.Sprintf(" after %d ms", m.Delay)
	}
	return s
}
