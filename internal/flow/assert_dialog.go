package flow

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// DialogTypes are the kinds of JavaScript dialog an assertDialog's type names.
var DialogTypes = []string{"alert", "confirm", "prompt", "beforeunload"}

// parseAssertDialog reads an assertDialog step (a Test Buddy extension for web flows, issue
// #91): a JavaScript dialog opened since the last step that acted on the page, as in
//
//	assertDialog: "Delete it?"                      # its message (the format's text rule)
//	assertDialog: {text: "Delete .*", type: confirm}
//	assertDialog                                    # any dialog ({} too)
//
// The map form takes text, type (alert, confirm, prompt or beforeunload), label and optional.
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
