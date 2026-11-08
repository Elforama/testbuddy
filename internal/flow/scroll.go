package flow

import (
	"strings"

	"gopkg.in/yaml.v3"
)

func (p *parser) parseScrollUntilVisible(c *Command, m *mapView, v *yaml.Node) error {
	var err error
	if m == nil {
		return p.errf(v, "scrollUntilVisible takes a map with element")
	}
	if err := m.commandOptions(c); err != nil {
		return err
	}
	el := m.get("element")
	if el == nil || isNull(el) {
		return p.errf(v, "scrollUntilVisible needs element")
	}
	if c.Selector, err = p.nestedSelector(el, "element"); err != nil {
		return err
	}
	if c.Selector.Optional {
		c.Optional = true
	}
	c.Direction = strings.ToUpper(m.str("direction"))
	if c.Direction == "" {
		c.Direction = "DOWN"
	}
	if c.Direction != "UP" && c.Direction != "DOWN" && c.Direction != "LEFT" && c.Direction != "RIGHT" {
		return p.errf(m.keys["direction"], "direction must be UP, DOWN, LEFT or RIGHT")
	}
	c.Timeout = m.str("timeout")
	c.Speed = m.str("speed")
	if c.VisibilityPct, err = m.integer("visibilityPercentage", 100); err != nil {
		return err
	}
	c.VisibilityPct = max(0, min(100, c.VisibilityPct))
	if c.CenterElement, err = m.boolean("centerElement"); err != nil {
		return err
	}
	if c.WaitToSettleTimeoutMs, err = m.integer("waitToSettleTimeoutMs", 0); err != nil {
		return err
	}
	return m.strict(c.Name)
}

func (p *parser) parseScrollUntilVisibleIn(c *Command, m *mapView, v, at *yaml.Node) error {
	var err error
	// Test Buddy extension. An element in a list inside the scrolling page (a shelf of cards,
	// a pane): it runs as two scrollUntilVisible steps. The page scrolls (pageDirection,
	// default DOWN) until `list`, any item of that inner list, is in view, then that list
	// scrolls (direction) until element shows, dragged across where `list` is, not guessed.
	if m == nil {
		return p.errf(v, "scrollUntilVisibleIn takes a map with list, element and direction")
	}
	if err := m.commandOptions(c); err != nil {
		return err
	}
	page := p.newCmd("scrollUntilVisible", at)
	inner := p.newCmd("scrollUntilVisible", at)
	for _, f := range []struct {
		key string
		to  **Selector
	}{{"list", &page.Selector}, {"element", &inner.Selector}} {
		sv := m.get(f.key)
		if sv == nil || isNull(sv) {
			return p.errf(v, "scrollUntilVisibleIn needs %s", f.key)
		}
		if *f.to, err = p.nestedSelector(sv, f.key); err != nil {
			return err
		}
	}
	inner.In = page.Selector
	for _, d := range []struct {
		key string
		to  *string
	}{{"direction", &inner.Direction}, {"pageDirection", &page.Direction}} {
		*d.to = strings.ToUpper(m.str(d.key))
		if *d.to == "" {
			*d.to = "DOWN"
		}
		if *d.to != "UP" && *d.to != "DOWN" && *d.to != "LEFT" && *d.to != "RIGHT" {
			return p.errf(m.keys[d.key], "%s must be UP, DOWN, LEFT or RIGHT", d.key)
		}
	}
	// The list item is brought in whole, so the inner list's drags run across it.
	page.VisibilityPct = 100
	inner.Timeout, page.Timeout = m.str("timeout"), m.str("timeout")
	inner.Speed, page.Speed = m.str("speed"), m.str("speed")
	if inner.VisibilityPct, err = m.integer("visibilityPercentage", 100); err != nil {
		return err
	}
	inner.VisibilityPct = max(0, min(100, inner.VisibilityPct))
	if inner.CenterElement, err = m.boolean("centerElement"); err != nil {
		return err
	}
	c.Selector, c.Direction, c.In = inner.Selector, inner.Direction, page.Selector
	c.Commands = []*Command{page, inner}
	return m.strict(c.Name)
}
