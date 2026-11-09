package flow

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// parseMockNetwork reads a mockNetwork step (a Test Buddy extension for web flows): a map
// with url (required), and method, status, body or file, headers and delay, as in
//
//	mockNetwork: {url: "*/api/items*", method: GET, body: '{"items": []}', delay: 1500}
//	mockNetwork: {url: "*/ads/*", fail: true}
func (p *parser) parseMockNetwork(c *Command, m *mapView, v, at *yaml.Node) error {
	if m == nil {
		where := v
		if where == nil {
			where = at
		}
		return p.errf(where, "mockNetwork takes a map: {url, method, status, body or file, headers, delay} or {url, fail: true}")
	}
	if err := m.commandOptions(c); err != nil {
		return err
	}
	mk := &NetworkMock{URL: m.str("url"), Method: strings.ToUpper(strings.TrimSpace(m.str("method")))}
	if mk.URL == "" {
		return p.errf(v, "mockNetwork needs url (a pattern: * matches any run of characters, ? one character)")
	}
	var err error
	if mk.Fail, err = m.boolean("fail"); err != nil {
		return err
	}
	if mk.Status, err = m.integer("status", 200); err != nil {
		return err
	}
	if mk.Status < 100 || mk.Status > 599 {
		return p.errf(m.keys["status"], "status must be an HTTP status code (100-599)")
	}
	if mk.Delay, err = m.integer("delay", 0); err != nil {
		return err
	}
	if mk.Delay < 0 {
		return p.errf(m.keys["delay"], "delay must not be negative (milliseconds)")
	}
	body, file := m.get("body"), m.get("file")
	if body != nil && file != nil {
		return p.errf(v, "mockNetwork takes body or file, not both")
	}
	if mk.Headers, err = p.kvs(m.get("headers")); err != nil {
		return err
	}
	if mk.Fail {
		for _, k := range []string{"body", "file", "headers", "status"} {
			if m.has(k) {
				return p.errf(m.knod[k], "mockNetwork with fail: true sends no response: drop %s", k)
			}
		}
	}
	if body != nil {
		mk.Body = scalarOr(body, "")
	}
	if file != nil {
		mk.BodyFile = p.resolve(scalarOr(file, ""))
		b, err := os.ReadFile(mk.BodyFile)
		if err != nil {
			return p.errf(file, "mock body file not found: %s", mk.BodyFile)
		}
		mk.Body, mk.BodyFromFile = string(b), true
	}
	c.Mock = mk
	return m.strict(c.Name)
}
