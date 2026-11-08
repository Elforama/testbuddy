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
