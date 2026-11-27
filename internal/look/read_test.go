package look

import (
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

func readOn(t *testing.T, s *ui.Screen, query string) (*Reading, bool) {
	t.Helper()
	return Build(s, NewRefs()).Read(query)
}

func expectReading(t *testing.T, s *ui.Screen, query, value, from string, matches int) {
	t.Helper()
	r, onScreen := readOn(t, s, query)
	if r == nil {
		t.Fatalf("Read(%q) found no value (label on screen: %v), want %q", query, onScreen, value)
	}
	if r.Value != value || r.From != from || r.Matches != matches {
		t.Errorf("Read(%q) = %q from %s, %d matches; want %q from %s, %d matches", query, r.Value, r.From, r.Matches, value, from, matches)
	}
}

func TestReadSwitchAndCheckBox(t *testing.T) {
	s := android(
		nd(1, "android.widget.Switch", row(0), text("Dark theme"), click, checkable),
		nd(1, "android.widget.CheckBox", row(1), text("Remember me"), click, checked),
		nd(1, "android.widget.Switch", row(2), desc("Wi-Fi"), click, checked),
	)
	expectReading(t, s, "Dark theme", "off", "switch", 1)
	expectReading(t, s, "remember me", "on", "switch", 1)
	expectReading(t, s, "Wi-Fi", "on", "switch", 1)
}

func TestReadField(t *testing.T) {
	s := android(
		nd(1, "android.widget.EditText", row(0), hint("Email"), text("bob@x.io")),
		nd(1, "android.widget.EditText", row(1), hint("Phone"), showingHint),
		nd(1, "android.widget.EditText", row(2), desc("City"), text("Lisbon")),
	)
	expectReading(t, s, "Email", "bob@x.io", "field", 1)
	expectReading(t, s, "Phone", "", "field", 1)
	expectReading(t, s, "City", "Lisbon", "field", 1)
	w := web(
		nd(1, "input:text", rect(100, 100, 600, 140), desc("Name"), text("Name"), editable, click),
		nd(1, "input:text", rect(100, 200, 600, 240), desc("Town"), asVal("Porto"), editable, click),
	)
	expectReading(t, w, "Name", "", "field", 1)
	expectReading(t, w, "Town", "Porto", "field", 1)
}

func TestReadRestOfLabel(t *testing.T) {
	s := android(
		nd(1, "android.widget.TextView", row(0), text("Subtotal: €20.00")),
		nd(1, "android.widget.TextView", row(1), text("Total: €24.99")),
		nd(1, "android.widget.TextView", row(3), text("Status - shipped")),
	)
	expectReading(t, s, "Total", "€24.99", "label", 1)
	expectReading(t, s, "Total:", "€24.99", "label", 1)
	expectReading(t, s, "status", "shipped", "label", 1)
}

func TestReadRightOnTheRow(t *testing.T) {
	s := android(
		nd(1, "android.widget.TextView", rect(40, 300, 500, 360), text("Total")),
		nd(1, "android.widget.ImageView", rect(520, 305, 560, 355)),
		nd(1, "android.widget.TextView", rect(800, 305, 1040, 355), text("$42")),
		nd(1, "android.widget.TextView", rect(40, 500, 500, 560), text("Dark theme")),
		nd(1, "android.widget.Switch", rect(900, 500, 1040, 560), click, checked),
		nd(1, "android.widget.TextView", rect(40, 700, 500, 760), text("Volume")),
		nd(1, "android.widget.SeekBar", rect(520, 700, 1040, 760), text("70%")),
	)
	expectReading(t, s, "Total", "$42", "right", 1)
	expectReading(t, s, "Dark theme", "on", "right", 1)
	expectReading(t, s, "Volume", "70%", "right", 1)
}

func TestReadDirectlyUnder(t *testing.T) {
	s := android(
		nd(1, "android.widget.TextView", rect(40, 600, 300, 660), text("Email")),
		nd(1, "android.widget.EditText", rect(40, 670, 1040, 770), text("bob@x.io")),
	)
	expectReading(t, s, "Email", "bob@x.io", "below", 1)
}

func TestReadRuleOrder(t *testing.T) {
	switchFirst := android(
		nd(1, "android.widget.TextView", rect(40, 200, 500, 260), text("Sync")),
		nd(1, "android.widget.TextView", rect(600, 200, 1040, 260), text("every hour")),
		nd(1, "android.widget.EditText", rect(40, 400, 1040, 500), hint("Sync"), text("typed")),
		nd(1, "android.widget.Switch", rect(40, 600, 1040, 700), text("Sync"), click, checked),
	)
	expectReading(t, switchFirst, "Sync", "on", "switch", 3)

	fieldBeforeLabel := android(
		nd(1, "android.widget.TextView", rect(40, 200, 1040, 260), text("Email: old@x.io")),
		nd(1, "android.widget.EditText", rect(40, 400, 1040, 500), hint("Email"), text("new@x.io")),
	)
	expectReading(t, fieldBeforeLabel, "Email", "new@x.io", "field", 2)

	labelBeforeRight := android(
		nd(1, "android.widget.TextView", rect(40, 200, 500, 260), text("Total: €24.99")),
		nd(1, "android.widget.TextView", rect(600, 200, 1040, 260), text("paid")),
	)
	expectReading(t, labelBeforeRight, "Total", "€24.99", "label", 1)

	rightBeforeUnder := android(
		nd(1, "android.widget.TextView", rect(40, 200, 500, 260), text("Total")),
		nd(1, "android.widget.TextView", rect(600, 200, 1040, 260), text("$42")),
		nd(1, "android.widget.TextView", rect(40, 270, 500, 330), text("incl. tax")),
	)
	expectReading(t, rightBeforeUnder, "Total", "$42", "right", 1)
}

func TestReadExactLabelBeatsALongerOne(t *testing.T) {
	s := android(
		nd(1, "android.widget.TextView", rect(40, 100, 1040, 160), text("Dark theme saves battery")),
		nd(1, "android.widget.TextView", rect(40, 300, 500, 360), text("Dark theme")),
		nd(1, "android.widget.Switch", rect(900, 300, 1040, 360), click, checkable),
	)
	expectReading(t, s, "Dark theme", "off", "right", 1)
}

func TestReadWholeWordsOnly(t *testing.T) {
	s := android(nd(1, "android.widget.TextView", row(0), text("Subtotal: €20.00")))
	if r, onScreen := readOn(t, s, "Total"); r != nil || onScreen {
		t.Errorf("Read(Total) = %+v, on screen %v; want nothing: Subtotal is another word", r, onScreen)
	}
}

func TestReadAmbiguousTakesTheFirst(t *testing.T) {
	s := android(
		nd(1, "android.widget.TextView", rect(40, 300, 500, 360), text("Price")),
		nd(1, "android.widget.TextView", rect(800, 300, 1040, 360), text("$1")),
		nd(1, "android.widget.TextView", rect(40, 500, 500, 560), text("Price")),
		nd(1, "android.widget.TextView", rect(800, 500, 1040, 560), text("$2")),
	)
	expectReading(t, s, "Price", "$1", "right", 2)
}

func TestReadLabelWithoutValueAndNotFound(t *testing.T) {
	s := android(
		nd(1, "android.widget.TextView", rect(40, 300, 500, 360), text("Summary")),
		nd(1, "android.widget.TextView", rect(40, 1500, 500, 1560), text("Unrelated")),
	)
	if r, onScreen := readOn(t, s, "Summary"); r != nil || !onScreen {
		t.Errorf("Read(Summary) = %+v, on screen %v; want no value for a label that is on screen", r, onScreen)
	}
	if r, onScreen := readOn(t, s, "Totals"); r != nil || onScreen {
		t.Errorf("Read(Totals) = %+v, on screen %v; want not on screen", r, onScreen)
	}
}

func TestReadKeepsLongValuesWhole(t *testing.T) {
	long := "A value that is much longer than the eighty characters a listing line keeps of a label, to its very end"
	s := android(
		nd(1, "android.widget.TextView", rect(40, 300, 500, 360), text("Notes")),
		nd(1, "android.widget.TextView", rect(520, 300, 1040, 360), text(long)),
	)
	expectReading(t, s, "Notes", long, "right", 1)
}

func TestReadOwnValue(t *testing.T) {
	s := web(
		nd(1, "input:range", rect(100, 100, 600, 140), text("Volume"), func(n *ui.Node) { n.Value = "50" }, click),
		nd(1, "div[progressbar]", rect(100, 200, 600, 240), desc("Upload"), func(n *ui.Node) { n.Value = "30%" }),
	)
	expectReading(t, s, "Volume", "50", "value", 1)
	expectReading(t, s, "Upload", "30%", "value", 1)
}

func TestReadDoesNotTakeAnotherLabelsSwitch(t *testing.T) {
	s := android(
		nd(1, "android.widget.TextView", rect(40, 300, 500, 360), text("Radio group")),
		nd(1, "android.widget.RadioButton", rect(40, 370, 300, 430), text("Red"), click),
		nd(1, "android.widget.TextView", rect(40, 600, 500, 660), text("Sync")),
		nd(1, "android.widget.Switch", rect(600, 600, 1040, 660), text("Wi-Fi only"), click, checked),
	)
	for _, q := range []string{"Radio group", "Sync"} {
		if r, onScreen := readOn(t, s, q); r != nil || !onScreen {
			t.Errorf("Read(%q) = %+v, on screen %v; want no value: the switch beside it has a label of its own", q, r, onScreen)
		}
	}
}

func TestReadByID(t *testing.T) {
	s := web(
		nd(1, "input:text", rect(100, 100, 600, 140), id("field_name"), asVal("Jon"), editable, click),
		nd(1, "p", rect(100, 200, 600, 240), id("status"), text("Hello, Jon")),
		nd(1, "input:checkbox", rect(100, 300, 120, 320), id("agree"), click, checked),
	)
	expectReading(t, s, "#field_name", "Jon", "id", 1)
	expectReading(t, s, "#status", "Hello, Jon", "id", 1)
	expectReading(t, s, "#agree", "on", "id", 1)
	if r, onScreen := readOn(t, s, "#missing"); r != nil || onScreen {
		t.Errorf("Read(#missing) = %+v, on screen %v", r, onScreen)
	}
}

func TestReadControlInsideItsLabel(t *testing.T) {
	s := web(
		nd(1, "label", rect(100, 100, 600, 140), text("Nickname"), click),
		nd(2, "input:text", rect(300, 100, 600, 140), id("nick"), asVal("Jo"), editable, click),
		nd(1, "button", rect(100, 150, 600, 190), text("Continue"), click),
		nd(1, "label", rect(100, 300, 600, 340), text("Updates"), click),
		nd(2, "input:checkbox", rect(100, 300, 120, 320), id("updates"), click, checked),
	)
	expectReading(t, s, "Nickname", "Jo", "inside", 1)
	expectReading(t, s, "Updates", "on", "inside", 1)
}

func TestReadDoesNotTakeAButtonOrHeadingForAValue(t *testing.T) {
	s := android(
		nd(1, "android.widget.TextView", rect(40, 300, 500, 360), text("Quantity")),
		nd(1, "android.widget.Button", rect(600, 300, 700, 360), text("Add"), click),
		nd(1, "android.widget.TextView", rect(40, 600, 500, 660), text("Nickname")),
		nd(1, "android.widget.Button", rect(40, 670, 500, 730), text("Continue"), click),
	)
	for _, q := range []string{"Quantity", "Nickname"} {
		if r, onScreen := readOn(t, s, q); r != nil || !onScreen {
			t.Errorf("Read(%q) = %+v, on screen %v; want no value: a button beside a label is not its value", q, r, onScreen)
		}
	}
}

func TestReadPrefersALabelThatStartsWithTheQuery(t *testing.T) {
	s := android(
		nd(1, "android.widget.Button", rect(40, 300, 400, 360), text("Ask camera"), click),
		nd(1, "android.widget.TextView", rect(500, 300, 1040, 360), text("Camera: not asked")),
	)
	expectReading(t, s, "Camera", "not asked", "label", 1)
	expectReading(t, s, "Ask camera", "Camera: not asked", "right", 1)
}

func TestReadExactLabelBeatsALongerSwitch(t *testing.T) {
	s := android(
		nd(1, "android.widget.TextView", rect(40, 300, 500, 360), text("Notifications")),
		nd(1, "android.widget.TextView", rect(600, 300, 1040, 360), text("3 unread")),
		nd(1, "android.widget.Switch", rect(40, 500, 1040, 560), text("Notifications sound"), click, checked),
	)
	expectReading(t, s, "Notifications", "3 unread", "right", 1)
	expectReading(t, s, "Notifications sound", "on", "switch", 1)
}
