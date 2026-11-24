package flow

import (
	"strings"

	"gopkg.in/yaml.v3"
)

func (p *parser) parseAnswerDialogs(c *Command, m *mapView, v, at *yaml.Node) error {
	const usage = "answerDialogs takes OK, CANCEL or a map: {button: OK|CANCEL, text}"
	ch := &DialogChoice{}
	button, where := "", v
	switch {
	case v != nil && v.Kind == yaml.ScalarNode && !isNull(v):
		button = v.Value
	case m != nil:
		if err := m.commandOptions(c); err != nil {
			return err
		}
		button, where = m.str("button"), m.get("button")
		if t := m.get("text"); t != nil {
			if t.Kind != yaml.ScalarNode {
				return p.errf(t, "answerDialogs' text is the text a prompt() gets")
			}
			ch.Text, ch.HasText = scalarOr(t, ""), true
		}
		if err := m.strict(c.Name); err != nil {
			return err
		}
	default:
		if where == nil {
			where = at
		}
		return p.errf(where, usage)
	}
	if button == "" && m == nil {
		return p.errf(where, usage)
	}
	if strings.Contains(button, "${") {
		ch.Button = button
		c.Dialogs = ch
		return nil
	}
	cancel, ok := DialogButton(button)
	if !ok {
		return p.errf(where, "%s (got %q)", usage, button)
	}
	if cancel && ch.HasText {
		return p.errf(m.knod["text"], "answerDialogs with CANCEL types no text: drop text")
	}
	ch.Cancel = cancel
	c.Dialogs = ch
	return nil
}

func DialogButton(s string) (cancel, ok bool) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "", "OK":
		return false, true
	case "CANCEL":
		return true, true
	}
	return false, false
}
