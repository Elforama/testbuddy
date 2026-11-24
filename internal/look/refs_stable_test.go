package look

import (
	"strconv"
	"testing"

	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

func labelled(l *Listing, label string) []*Element {
	var out []*Element
	for _, e := range l.Elements {
		if e.Label == label {
			out = append(out, e)
		}
	}
	return out
}

func expectSelectors(t *testing.T, s *ui.Screen, l *Listing, label string, want ...string) {
	t.Helper()
	els := labelled(l, label)
	if len(els) != len(want) {
		t.Fatalf("%d elements labelled %q, want %d:\n%s", len(els), label, len(want), l.Text())
	}
	for i, e := range els {
		got := SelectorFor(s, e, false)
		if got != want[i] {
			t.Errorf("%q #%d: selector = %s, want %s", label, i, got, want[i])
			continue
		}
		findsAgain(t, s, e, got)
	}
}

func findsAgain(t *testing.T, s *ui.Screen, e *Element, src string) {
	t.Helper()
	sel, ok := parseSelector(t, src)
	if !ok {
		return
	}
	got, _, err := match.Find(s, sel)
	if err != nil || got == nil {
		t.Errorf("selector %s finds nothing (%v)", src, err)
		return
	}
	if got != e.Node && !got.IsDescendantOf(e.Node) && !e.Node.IsDescendantOf(got) {
		t.Errorf("selector %s finds %s %v, not the element at %v", src, got.ShortClass(), got.Bounds, e.Node.Bounds)
	}
}

func TestSelectorForIDAndText(t *testing.T) {
	s := android(
		nd(1, "android.widget.Button", row(0), id("com.example.app:id/action"), text("Save"), click),
		nd(1, "android.widget.Button", row(1), id("com.example.app:id/action"), text("Cancel"), click),
	)
	l := Build(s, NewRefs())
	expectSelectors(t, s, l, "Save", `{id: "action", text: "Save"}`)
	expectSelectors(t, s, l, "Cancel", `{id: "action", text: "Cancel"}`)
}

func TestSelectorForRowsByTitle(t *testing.T) {
	var nodes []*ui.Node
	for i, title := range []string{"Milk", "Eggs", "Bread"} {
		r := row(i)
		nodes = append(nodes,
			nd(1, "android.widget.LinearLayout", r),
			nd(2, "android.widget.TextView", rect(20, r.T+10, 500, r.B-10), text(title)),
			nd(2, "android.widget.Button", rect(800, r.T+10, 1060, r.B-10), text("Edit"), click),
		)
	}
	s := android(nodes...)
	l := Build(s, NewRefs())
	expectSelectors(t, s, l, "Edit",
		`{text: "Edit", childOf: {containsChild: "Milk"}}`,
		`{text: "Edit", childOf: {containsChild: "Eggs"}}`,
		`{text: "Edit", childOf: {containsChild: "Bread"}}`,
	)
}

func TestSelectorForRowsByID(t *testing.T) {
	var nodes []*ui.Node
	for i, name := range []string{"milk", "eggs"} {
		r := row(i)
		nodes = append(nodes,
			nd(1, "android.widget.LinearLayout", r, id("com.example.app:id/row_"+name)),
			nd(2, "android.widget.Button", rect(800, r.T+10, 1060, r.B-10), id("com.example.app:id/edit"), text("Edit"), click),
		)
	}
	s := android(nodes...)
	l := Build(s, NewRefs())
	expectSelectors(t, s, l, "Edit", `{id: "edit", childOf: {id: "row_milk"}}`, `{id: "edit", childOf: {id: "row_eggs"}}`)
}

func TestSelectorForBelowTitle(t *testing.T) {
	s := android(
		nd(1, "android.widget.TextView", row(0), text("Milk")),
		nd(1, "android.widget.Button", row(1), text("Edit"), click),
		nd(1, "android.widget.TextView", row(2), text("Eggs")),
		nd(1, "android.widget.Button", row(3), text("Edit"), click),
	)
	l := Build(s, NewRefs())
	expectSelectors(t, s, l, "Edit", `{text: "Edit", below: "Milk"}`, `{text: "Edit", below: "Eggs"}`)
}

func TestSelectorForUnlabelledInToolbar(t *testing.T) {
	s := android(
		nd(1, "android.view.ViewGroup", rect(0, 0, 1080, 150), id("com.example.app:id/toolbar")),
		nd(2, "android.widget.TextView", rect(150, 40, 700, 110), text("Inbox")),
		nd(2, "android.widget.ImageButton", rect(980, 30, 1070, 120), click),
		nd(1, "android.widget.ImageButton", rect(980, 2200, 1070, 2290), click),
	)
	l := Build(s, NewRefs())
	expectSelectors(t, s, l, "", `{childOf: {id: "toolbar"}}`, `{point: "94%,93%"}`)
}

func TestSelectorForStillFallsBack(t *testing.T) {
	s := android(
		nd(1, "android.view.ViewGroup", rect(0, 0, 1080, 150), id("com.example.app:id/toolbar")),
		nd(2, "android.widget.ImageButton", rect(880, 30, 970, 120), click),
		nd(2, "android.widget.ImageButton", rect(980, 30, 1070, 120), click),
		nd(1, "android.widget.Button", row(1), text("Remove"), click),
		nd(1, "android.widget.Button", row(2), text("Remove"), click),
	)
	l := Build(s, NewRefs())
	expectSelectors(t, s, l, "", `{point: "85%,3%"}`, `{point: "94%,3%"}`)
	expectSelectors(t, s, l, "Remove", `"Remove"`, `{text: "Remove", index: 1}`)
}

func TestSelectorForCheckRowsByID(t *testing.T) {
	var nodes []*ui.Node
	for i, name := range []string{"milk", "eggs"} {
		r := row(i)
		nodes = append(nodes,
			nd(1, "android.widget.LinearLayout", r, id("com.example.app:id/row_"+name)),
			nd(2, "android.widget.ImageView", rect(800, r.T+10, 1060, r.B-10), id("com.example.app:id/badge"), click),
		)
	}
	s := android(nodes...)
	l := Build(s, NewRefs())
	var badges []*Element
	for _, e := range l.Elements {
		if e.Node.ID == "com.example.app:id/badge" {
			badges = append(badges, e)
		}
	}
	if len(badges) != 2 {
		t.Fatalf("badges: %d\n%s", len(badges), l.Text())
	}
	if got := SelectorFor(s, badges[1], true); got != `{id: "badge", childOf: {id: "row_eggs"}}` {
		t.Errorf("check selector = %s", got)
	}
}

func TestSelectorForFirstOfSeveral(t *testing.T) {
	var nodes []*ui.Node
	var want []string
	for i := 0; i < 30; i++ {
		r := row(i)
		title := "Item " + strconv.Itoa(i) + " title"
		nodes = append(nodes,
			nd(1, "android.widget.LinearLayout", r),
			nd(2, "android.widget.TextView", rect(20, r.T+10, 500, r.B-10), text(title)),
			nd(2, "android.widget.Button", rect(800, r.T+10, 1060, r.B-10), text("Edit"), click),
		)
		if r.B <= 2400 {
			want = append(want, `{text: "Edit", childOf: {containsChild: "`+title+`"}}`)
		}
	}
	s := android(nodes...)
	l := Build(s, NewRefs())
	expectSelectors(t, s, l, "Edit", want...)
	if got := SelectorFor(s, labelled(l, "Edit")[0], true); got != want[0] {
		t.Errorf("check selector = %s, want %s", got, want[0])
	}
}
