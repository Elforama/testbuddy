package flow

import (
	"strings"

	"gopkg.in/yaml.v3"
)

var DialogTypes = []string{"alert", "confirm", "prompt", "beforeunload"}

func (p *parser) parseAssertDialog(c *Command, m *mapView, v *yaml.Node) error {
	ch := &DialogCheck{}
	switch {
	case v == nil || isNull(v):
	case v.Kind == yaml.ScalarNode:
		ch.Text, ch.HasText = v.Value, true
	case m != nil:
		if err := m.commandOptions(c); err != nil {
			return err
		}
		if t := m.get("text"); t != nil {
			if t.Kind != yaml.ScalarNode {
				return p.errf(t, "assertDialog's text is the dialog's message (a text or a regex)")
			}
			ch.Text, ch.HasText = scalarOr(t, ""), true
		}
		if t := m.get("type"); t != nil {
			typ := strings.ToLower(strings.TrimSpace(t.Value))
			ok := false
			for _, k := range DialogTypes {
				ok = ok || typ == k
			}
			if t.Kind != yaml.ScalarNode || !ok {
				return p.errf(t, "assertDialog's type is one of %s (got %q)", strings.Join(DialogTypes, ", "), t.Value)
			}
			ch.Type = typ
		}
		if err := m.strict(c.Name); err != nil {
			return err
		}
	default:
		return p.errf(v, "assertDialog takes the dialog's text or a map: {text, type}")
	}
	c.DialogCheck = ch
	return nil
}
