package flow

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// parseAnswerDialogs reads an answerDialogs step (a Test Buddy extension for web flows,
// issue #91): how the page's JavaScript confirm() and prompt() dialogs are answered from then
// on, as in
//
//	answerDialogs: CANCEL            # confirm() returns false, prompt() null
//	answerDialogs: {text: "Grace"}   # prompt() returns "Grace" (OK)
//	answerDialogs: OK                # tb's default again
//
// The map form takes button (OK or CANCEL), text, label and optional.
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
	switch strings.ToUpper(strings.TrimSpace(button)) {
	case "", "OK":
	case "CANCEL":
		ch.Cancel = true
		if ch.HasText {
			return p.errf(m.knod["text"], "answerDialogs with CANCEL types no text: drop text")
		}
	default:
		return p.errf(where, "%s (got %q)", usage, button)
	}
	if button == "" && m == nil {
		return p.errf(where, usage)
	}
	c.Dialogs = ch
	return nil
}
