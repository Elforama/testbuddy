package flow

import (
	"os"

	"gopkg.in/yaml.v3"
)

func (p *parser) parseRunFlow(c *Command, m *mapView, v *yaml.Node) error {
	if v != nil && v.Kind == yaml.ScalarNode {
		return p.subFile(c, v, v.Value)
	}
	if m == nil {
		return nil
	}
	if err := m.commandOptions(c); err != nil {
		return err
	}
	if err := p.parseFileOrCommands(c, m, v); err != nil {
		return err
	}
	var err error
	if c.Env, err = p.env(m.get("env")); err != nil {
		return err
	}
	if w := m.get("when"); w != nil && !isNull(w) {
		if c.When, err = p.condition(w); err != nil {
			return err
		}
	}
	return m.strict(c.Name)
}

func (p *parser) parseRunScript(c *Command, m *mapView, v *yaml.Node) error {
	var err error
	var file *yaml.Node
	if v != nil && v.Kind == yaml.ScalarNode {
		file = v
	} else if m != nil {
		if err := m.commandOptions(c); err != nil {
			return err
		}
		file = m.get("file")
		if file == nil {
			return p.errf(v, "runScript needs file")
		}
		if c.Env, err = p.env(m.get("env")); err != nil {
			return err
		}
		if w := m.get("when"); w != nil && !isNull(w) {
			if c.When, err = p.condition(w); err != nil {
				return err
			}
		}
		if err := m.strict(c.Name); err != nil {
			return err
		}
	}
	c.SubFile = p.resolve(file.Value)
	src, err := os.ReadFile(c.SubFile)
	if err != nil {
		return p.errf(file, "script file not found: %s", c.SubFile)
	}
	c.Script = string(src)
	return nil
}

func (p *parser) parseRepeat(c *Command, m *mapView, v *yaml.Node) error {
	var err error
	if m == nil {
		return p.errf(v, "repeat takes a map with commands, and times and/or while")
	}
	if err := m.commandOptions(c); err != nil {
		return err
	}
	c.Times = m.str("times")
	if w := m.get("while"); w != nil && !isNull(w) {
		if c.While, err = p.condition(w); err != nil {
			return err
		}
	}
	cmds := m.get("commands")
	if cmds == nil || cmds.Kind != yaml.SequenceNode {
		return p.errf(v, "repeat needs commands")
	}
	if c.Commands, err = p.commands(cmds); err != nil {
		return err
	}
	return m.strict(c.Name)
}

func (p *parser) parseRetry(c *Command, m *mapView, v *yaml.Node) error {
	if m == nil {
		return p.errf(v, "retry takes a map with commands or file")
	}
	if err := m.commandOptions(c); err != nil {
		return err
	}
	c.MaxRetries = m.str("maxRetries")
	if err := p.parseFileOrCommands(c, m, v); err != nil {
		return err
	}
	var err error
	if c.Env, err = p.env(m.get("env")); err != nil {
		return err
	}
	return m.strict(c.Name)
}

func (p *parser) parseFileOrCommands(c *Command, m *mapView, v *yaml.Node) error {
	file, cmds := m.get("file"), m.get("commands")
	if (file == nil) == (cmds == nil) {
		return p.errf(v, "%s needs exactly one of file or commands", c.Name)
	}
	if file != nil {
		return p.subFile(c, file, file.Value)
	}
	if cmds.Kind != yaml.SequenceNode {
		return p.errf(cmds, "commands must be a list")
	}
	var err error
	c.Commands, err = p.commands(cmds)
	return err
}

func (p *parser) parseParallel(c *Command, m *mapView, v, at *yaml.Node) error {
	if !p.multi {
		return p.errf(at, "parallel is only for multi-device flows (declare devices: in the config)")
	}
	if m == nil {
		return p.errf(v, "parallel maps device roles to their commands")
	}
	c.Parallel = map[string][]*Command{}
	for i := 0; i+1 < len(v.Content); i += 2 {
		role, body := v.Content[i], v.Content[i+1]
		if !p.roles[role.Value] {
			return p.errf(role, "unknown device role %q", role.Value)
		}
		blk, err := p.roleBlock(role.Value, body, role)
		if err != nil {
			return err
		}
		c.Parallel[role.Value] = blk.Commands
		c.ParallelOrder = append(c.ParallelOrder, role.Value)
	}
	return nil
}

func (p *parser) subFile(c *Command, at *yaml.Node, path string) error {
	abs := p.resolve(path)
	st, err := os.Stat(abs)
	if err != nil {
		return p.errf(at, "flow file not found: %s", abs)
	}
	if st.IsDir() {
		return p.errf(at, "%s is a directory, not a flow file", abs)
	}
	if abs == p.file {
		return p.errf(at, "a flow cannot run itself")
	}
	sub, err := parseFile(abs, p.stack)
	if err != nil {
		return err
	}
	c.SubFile = abs
	c.SubFlow = sub
	return nil
}
