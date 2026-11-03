package main

import (
	"fmt"

	"github.com/elforama/testbuddy/internal/ui"
)

// hnode is the full hierarchy as JSON (tb hierarchy, debug output), in the usual attribute
// vocabulary.
type hnode struct {
	Attributes map[string]string `json:"attributes"`
	Children   []*hnode          `json:"children,omitempty"`
	Clickable  *bool             `json:"clickable,omitempty"`
	Enabled    *bool             `json:"enabled,omitempty"`
	Focused    *bool             `json:"focused,omitempty"`
	Checked    *bool             `json:"checked,omitempty"`
	Selected   *bool             `json:"selected,omitempty"`
}

func bp(b bool) *bool { return &b }

func toH(n *ui.Node) *hnode {
	attrs := map[string]string{"bounds": n.Bounds.String()}
	set := func(k, v string) {
		if v != "" {
			attrs[k] = v
		}
	}
	set("text", n.Text)
	set("accessibilityText", n.Desc)
	set("resource-id", n.ID)
	set("hintText", n.Hint)
	set("class", n.Class)
	set("error", n.Error)
	set("value", n.Value)
	if n.Coll != nil {
		set("collection", fmt.Sprintf("%d,%d", n.Coll.Rows, n.Coll.Cols))
	}
	if n.Item != nil {
		set("collectionItem", fmt.Sprintf("%d,%d", n.Item.Rows, n.Item.Cols))
	}
	if n.MoreBefore {
		set("canScrollBackward", "true")
	}
	if n.MoreAfter {
		set("canScrollForward", "true")
	}
	h := &hnode{Attributes: attrs, Clickable: bp(n.Clickable), Enabled: bp(n.Enabled), Focused: bp(n.Focused),
		Checked: bp(n.Checked), Selected: bp(n.Selected)}
	for _, c := range n.Children {
		h.Children = append(h.Children, toH(c))
	}
	return h
}

func hierarchyJSON(s *ui.Screen) *hnode {
	root := &hnode{Attributes: map[string]string{"bounds": ui.Rect{R: s.Width, B: s.Height}.String()}}
	for _, w := range s.Windows {
		win := &hnode{Attributes: map[string]string{"window": w.Type, "package": w.Package, "bounds": w.Bounds.String()}}
		for _, r := range w.Roots {
			win.Children = append(win.Children, toH(r))
		}
		root.Children = append(root.Children, win)
	}
	return root
}
