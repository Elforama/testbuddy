package flow

import "gopkg.in/yaml.v3"

func (p *parser) parseTap(c *Command, v *yaml.Node) error {
	if v.Kind == yaml.ScalarNode {
		c.Selector = &Selector{Text: v.Value}
	} else {
		s, sm, err := p.selector(v)
		if err != nil {
			return err
		}
		c.Selector = s
		c.Label = sm.str("label")
		if c.Optional, err = sm.boolean("optional"); err != nil {
			return err
		}
		c.Point = sm.str("point")
		if c.RetryTapIfNoChange, err = sm.boolean("retryTapIfNoChange"); err != nil {
			return err
		}
		if c.WaitUntilVisible, err = sm.boolean("waitUntilVisible"); err != nil {
			return err
		}
		if c.Repeat, err = sm.integer("repeat", 0); err != nil {
			return err
		}
		if c.Delay, err = sm.integer("delay", 100); err != nil {
			return err
		}
		if c.WaitToSettleTimeoutMs, err = sm.integer("waitToSettleTimeoutMs", 0); err != nil {
			return err
		}
		if err := sm.strict(c.Name); err != nil {
			return err
		}
		if !s.HasCriteria() && c.Point == "" {
			// {optional: true} alone: matches every node (the first clickable is picked)
		}
		if !s.HasCriteria() && c.Point != "" {
			c.Selector = nil // a screen point tap
		}
	}
	if c.Delay < 0 {
		c.Delay = 100
	}
	if c.WaitToSettleTimeoutMs > 30000 {
		c.WaitToSettleTimeoutMs = 30000
	}
	switch c.Name {
	case "doubleTapOn":
		c.Repeat = 2
	case "longPressOn":
		c.LongPress = true
		c.Repeat = 0
	}
	if c.Selector != nil && c.Selector.Optional {
		c.Optional = true
	}
	return nil
}
