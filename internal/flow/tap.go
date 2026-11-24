package flow

import (
	"math"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

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
		if c.RetryTapIfNoChange, err = sm.boolPtr("retryTapIfNoChange"); err != nil {
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
		if v := sm.get("timeout"); v != nil && !isNull(v) {
			if c.Timeout, err = tapTimeout(p, v); err != nil {
				return err
			}
		}
		if err := sm.strict(c.Name); err != nil {
			return err
		}
		if !s.HasCriteria() && c.Point == "" {
		}
		if !s.HasCriteria() && c.Point != "" {
			c.Selector = nil
			if c.Timeout != "" {
				return p.errf(sm.get("timeout"), "%s: timeout is how long to look for the element; a point tap looks for none", c.Name)
			}
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

func tapTimeout(p *parser, v *yaml.Node) (string, error) {
	t := strings.TrimSpace(v.Value)
	if v.Kind != yaml.ScalarNode || t == "" {
		return "", p.errf(v, "timeout is a number of milliseconds, 0 or more")
	}
	if strings.Contains(t, "${") {
		return t, nil
	}
	ms, err := strconv.ParseFloat(strings.ReplaceAll(t, "_", ""), 64)
	if err != nil || ms < 0 || math.IsNaN(ms) || math.IsInf(ms, 0) {
		return "", p.errf(v, "timeout %q is not a number of milliseconds, 0 or more", t)
	}
	return t, nil
}
