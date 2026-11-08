package flow

import (
	"strings"

	"gopkg.in/yaml.v3"
)

var selectorKeys = []string{"text", "id", "index", "width", "height", "tolerance", "enabled", "selected",
	"checked", "focused", "below", "above", "leftOf", "rightOf", "containsChild", "containsDescendants",
	"childOf", "traits", "css"}

// Command options that live inside a selector map. Accepted (and ignored) in nested selectors.
var tapOptionKeys = []string{"optional", "label", "point", "retryTapIfNoChange", "waitUntilVisible",
	"repeat", "delay", "waitToSettleTimeoutMs"}

// selector parses a selector: a scalar is a text regex; a map has selector keys.
// Command options found in the map are left unread for the caller.
func (p *parser) selector(n *yaml.Node) (*Selector, *mapView, error) {
	if n.Kind == yaml.ScalarNode {
		return &Selector{Text: n.Value}, nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, nil, p.errf(n, "a selector is a text or a map (text, id, index, ...)")
	}
	m := p.mapOf(n)
	s := &Selector{}
	var err error
	s.Text = m.str("text")
	s.ID = m.str("id")
	s.Index = m.str("index")
	s.CSS = m.str("css")
	if s.Width, err = m.intPtr("width"); err != nil {
		return nil, nil, err
	}
	if s.Height, err = m.intPtr("height"); err != nil {
		return nil, nil, err
	}
	if s.Tolerance, err = m.intPtr("tolerance"); err != nil {
		return nil, nil, err
	}
	for _, f := range []struct {
		k string
		p **bool
	}{{"enabled", &s.Enabled}, {"selected", &s.Selected}, {"checked", &s.Checked}, {"focused", &s.Focused}} {
		if *f.p, err = m.boolPtr(f.k); err != nil {
			return nil, nil, err
		}
	}
	if t := m.get("traits"); t != nil && !isNull(t) {
		for _, w := range strings.Fields(t.Value) {
			w = strings.ToLower(strings.ReplaceAll(w, "_", "-"))
			if w != "text" && w != "square" && w != "long-text" {
				return nil, nil, p.errf(t, "unknown trait %q (text, square, long-text)", w)
			}
			s.Traits = append(s.Traits, w)
		}
	}
	for _, rel := range []struct {
		k string
		p **Selector
	}{{"below", &s.Below}, {"above", &s.Above}, {"leftOf", &s.LeftOf}, {"rightOf", &s.RightOf},
		{"containsChild", &s.ContainsChild}, {"childOf", &s.ChildOf}} {
		if v := m.get(rel.k); v != nil && !isNull(v) {
			sub, sm, err := p.selector(v)
			if err != nil {
				return nil, nil, err
			}
			if sm != nil {
				for _, k := range tapOptionKeys {
					sm.get(k)
				}
				if err := sm.strict(rel.k); err != nil {
					return nil, nil, err
				}
			}
			*rel.p = sub
		}
	}
	if v := m.get("containsDescendants"); v != nil && !isNull(v) {
		if v.Kind != yaml.SequenceNode {
			return nil, nil, p.errf(v, "containsDescendants is a list of selectors")
		}
		for _, item := range v.Content {
			sub, sm, err := p.selector(item)
			if err != nil {
				return nil, nil, err
			}
			if sm != nil {
				for _, k := range tapOptionKeys {
					sm.get(k)
				}
				if err := sm.strict("containsDescendants"); err != nil {
					return nil, nil, err
				}
			}
			s.ContainsDescendants = append(s.ContainsDescendants, sub)
		}
	}
	if o := m.keys["optional"]; o != nil && !isNull(o) {
		s.Optional, _ = p.parseBool(o)
	}
	return s, m, nil
}

// nestedSelector parses a selector used as a value (element:, from:, cropOn:, visible:).
func (p *parser) nestedSelector(n *yaml.Node, where string) (*Selector, error) {
	s, m, err := p.selector(n)
	if err != nil {
		return nil, err
	}
	if m != nil {
		for _, k := range tapOptionKeys {
			m.get(k)
		}
		if err := m.strict(where); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (p *parser) condition(n *yaml.Node) (*Condition, error) {
	if n.Kind != yaml.MappingNode {
		return nil, p.errf(n, "a condition is a map (visible, notVisible, true, platform)")
	}
	m := p.mapOf(n)
	c := &Condition{Label: m.str("label")}
	m.get("optional")
	c.Timeout = m.str("timeout") // Test Buddy extension: the condition's own wait
	if v := m.get("platform"); v != nil && !isNull(v) {
		pl := strings.ToLower(v.Value)
		if pl != "android" && pl != "ios" && pl != "web" {
			return nil, p.errf(v, "platform must be Android, iOS or Web")
		}
		c.Platform = pl
	}
	var err error
	if v := m.get("visible"); v != nil && !isNull(v) {
		if c.Visible, err = p.nestedSelector(v, "visible"); err != nil {
			return nil, err
		}
	}
	if v := m.get("notVisible"); v != nil && !isNull(v) {
		if c.NotVisible, err = p.nestedSelector(v, "notVisible"); err != nil {
			return nil, err
		}
	}
	if v := m.get("true"); v != nil {
		s := scalarOr(v, "")
		c.True = &s
	}
	return c, m.strict("condition")
}
