package flow

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func (p *parser) swipe(c *Command, m *mapView, v *yaml.Node) error {
	c.Label = strings.ReplaceAll(m.str("label"), `"`, "")
	if o := m.get("optional"); o != nil {
		c.Optional = strings.EqualFold(o.Value, "true")
	}
	c.Duration = 400
	if d := m.get("duration"); d != nil && !isNull(d) {
		n, err := p.parseInt(d)
		if err != nil {
			return err
		}
		c.Duration = n
	}
	if w := m.get("waitToSettleTimeoutMs"); w != nil {
		c.WaitToSettleTimeoutMs, _ = strconv.Atoi(strings.TrimSpace(w.Value))
	}
	start, end, dir, from := m.get("start"), m.get("end"), m.get("direction"), m.get("from")
	switch {
	case start != nil || end != nil:
		if dir != nil {
			return p.errf(v, "swipe takes either direction or start/end, not both")
		}
		if start == nil || end == nil {
			return p.errf(v, "swipe needs both start and end")
		}
		c.Start, c.End = start.Value, end.Value
		if strings.Contains(c.Start, "%") != strings.Contains(c.End, "%") {
			return p.errf(v, "swipe start and end must both be percentages or both be coordinates")
		}
	case dir != nil && from == nil:
		if dir.Value != strings.ToUpper(dir.Value) && dir.Value != strings.ToLower(dir.Value) {
			return p.errf(dir, "swipe direction must be UP, DOWN, LEFT or RIGHT (all upper or all lower case)")
		}
		c.Direction = strings.ToUpper(dir.Value)
	case dir != nil && from != nil:
		c.Direction = strings.ToUpper(dir.Value)
		s, sm, err := p.selector(from)
		if err != nil {
			return err
		}
		if sm != nil {
			c.Point = sm.str("point")
			for _, k := range tapOptionKeys {
				sm.get(k)
			}
			if err := sm.strict("from"); err != nil {
				return err
			}
		}
		c.From = s
		if s.Optional {
			c.Optional = true
		}
		if err := m.strict("swipe"); err != nil {
			return err
		}
	default:
		return p.errf(v, "Swipe command takes either: direction, start and end, or from and direction")
	}
	switch c.Direction {
	case "", "UP", "DOWN", "LEFT", "RIGHT":
	default:
		return p.errf(dir, "swipe direction must be UP, DOWN, LEFT or RIGHT")
	}
	return nil
}

const DefaultDragHold = 800

const DefaultDragDuration = 1000

func (p *parser) drag(c *Command, m *mapView, v *yaml.Node) error {
	if err := m.commandOptions(c); err != nil {
		return err
	}
	var err error
	if c.Hold, err = m.integer("hold", DefaultDragHold); err != nil {
		return err
	}
	if c.Duration, err = m.integer("duration", DefaultDragDuration); err != nil {
		return err
	}
	if c.Hold < 0 || c.Duration < 0 {
		return p.errf(v, "drag's hold and duration are milliseconds, 0 or more")
	}
	if c.WaitToSettleTimeoutMs, err = m.integer("waitToSettleTimeoutMs", 0); err != nil {
		return err
	}
	ends := []struct {
		sel, pt string
		to      **Selector
		at      *string
	}{{"from", "start", &c.From, &c.Start}, {"to", "end", &c.To, &c.End}}
	for _, e := range ends {
		sv, pv := m.get(e.sel), m.get(e.pt)
		if sv != nil && isNull(sv) {
			sv = nil
		}
		if pv != nil && isNull(pv) {
			pv = nil
		}
		switch {
		case sv != nil && pv != nil:
			return p.errf(v, "drag takes %s or %s, not both", e.sel, e.pt)
		case sv != nil:
			s, sm, err := p.selector(sv)
			if err != nil {
				return err
			}
			if sm != nil {
				sm.get("optional")
				if err := sm.strict(e.sel); err != nil {
					return err
				}
			}
			if s.Optional {
				c.Optional = true
			}
			*e.to = s
		case pv != nil:
			*e.at = pv.Value
		default:
			return p.errf(v, "drag needs %s (an element) or %s (a point)", e.sel, e.pt)
		}
	}
	if c.Start != "" && c.End != "" && strings.Contains(c.Start, "%") != strings.Contains(c.End, "%") {
		return p.errf(v, "drag start and end must both be percentages or both be coordinates")
	}
	return m.strict("drag")
}
