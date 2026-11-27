package look

import (
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

const textView = "android.widget.TextView"

func expectNoValue(t *testing.T, s *ui.Screen, queries ...string) {
	t.Helper()
	for _, q := range queries {
		if r, onScreen := readOn(t, s, q); r != nil || !onScreen {
			t.Errorf("Read(%q) = %+v, on screen %v; want no value for a label that is on screen", q, r, onScreen)
		}
	}
}

func TestReadPlainTextUnderALabelIsNotItsValue(t *testing.T) {
	expectNoValue(t, android(
		nd(1, textView, rect(40, 300, 500, 360), text("Account")),
		nd(1, textView, rect(40, 400, 500, 460), text("Privacy")),
		nd(1, textView, rect(40, 500, 500, 560), text("About")),
	), "Account", "Privacy", "About")
	expectNoValue(t, android(
		nd(1, "android.widget.Switch", rect(40, 300, 180, 360), click, checked),
		nd(1, textView, rect(600, 300, 1040, 360), text("Dark theme")),
		nd(1, "android.widget.Switch", rect(40, 400, 180, 460), click, checkable),
		nd(1, textView, rect(600, 400, 1040, 460), text("Sync")),
		nd(1, textView, rect(40, 500, 300, 560), text("€5")),
		nd(1, textView, rect(600, 500, 1040, 560), text("Total")),
	), "Dark theme", "Sync", "Total")
	expectNoValue(t, android(
		nd(1, textView, rect(40, 300, 500, 360), text("Password")),
		nd(1, "android.widget.ImageButton", rect(900, 300, 1000, 360), desc("Show"), click),
		nd(1, textView, rect(40, 400, 500, 460), text("Email")),
		nd(1, textView, rect(600, 400, 1040, 460), text("a@b.c")),
	), "Password")
	expectNoValue(t, ios(
		nd(2, "Table", rect(0, 100, 390, 800)),
		nd(3, "Cell", rect(0, 188, 390, 232)),
		nd(4, "StaticText", rect(16, 198, 150, 222), desc("General")),
		nd(3, "Cell", rect(0, 232, 390, 276)),
		nd(4, "StaticText", rect(16, 242, 150, 266), desc("Privacy")),
	), "General")
}

func TestReadUnderTakesOnlyAControl(t *testing.T) {
	s := android(
		nd(1, textView, rect(40, 300, 300, 360), text("Email")),
		nd(1, "android.widget.EditText", rect(40, 370, 1040, 470), text("bob@x.io")),
		nd(1, textView, rect(40, 600, 300, 660), text("Volume")),
		nd(1, "android.widget.SeekBar", rect(40, 670, 1040, 730), text("70%")),
		nd(1, textView, rect(40, 900, 300, 960), text("Address")),
		nd(1, textView, rect(40, 970, 900, 1030), text("12 Harbour Road")),
		nd(1, "android.widget.EditText", rect(40, 1040, 1040, 1140), text("not the address")),
	)
	expectReading(t, s, "Email", "bob@x.io", "below", 1)
	expectReading(t, s, "Volume", "70%", "below", 1)
	expectNoValue(t, s, "Address")
}

func TestReadRightOnlyOnALabelAndValueRow(t *testing.T) {
	header := android(
		nd(1, textView, rect(40, 300, 300, 360), text("Name")),
		nd(1, textView, rect(400, 300, 700, 360), text("Email")),
		nd(1, textView, rect(800, 300, 1040, 360), text("Role")),
		nd(1, textView, rect(40, 400, 300, 460), text("Ann")),
		nd(1, textView, rect(400, 400, 700, 460), text("ann@x.io")),
	)
	expectNoValue(t, header, "Name", "Email", "Role")
	expectReading(t, header, "Ann", "ann@x.io", "right", 1)

	columns := android(
		nd(1, textView, rect(40, 300, 400, 360), text("Bluetooth")),
		nd(1, textView, rect(560, 310, 800, 370), text("Wi-Fi")),
		nd(1, "android.widget.Switch", rect(900, 310, 1040, 370), click, checked),
	)
	expectNoValue(t, columns, "Bluetooth")
	expectReading(t, columns, "Wi-Fi", "on", "right", 1)

	withEdit := android(
		nd(1, textView, rect(40, 300, 400, 360), text("Total")),
		nd(1, textView, rect(560, 300, 800, 360), text("$42")),
		nd(1, "android.widget.Button", rect(900, 300, 1040, 360), text("Edit"), click),
	)
	expectReading(t, withEdit, "Total", "$42", "right", 1)
}

func TestReadRightStaysInsideTheRowContainer(t *testing.T) {
	expectNoValue(t, android(
		nd(1, "android.view.ViewGroup", rect(0, 280, 540, 380)),
		nd(2, textView, rect(40, 300, 400, 360), text("Bluetooth")),
		nd(1, "android.view.ViewGroup", rect(540, 280, 1080, 380)),
		nd(2, "android.widget.Switch", rect(900, 300, 1040, 360), click, checked),
	), "Bluetooth")
	cells := ios(
		nd(2, "Table", rect(0, 100, 390, 800)),
		nd(3, "Cell", rect(0, 100, 390, 144)),
		nd(4, "StaticText", rect(16, 110, 150, 134), desc("Language")),
		nd(4, "StaticText", rect(250, 110, 350, 134), desc("English")),
	)
	r, _ := readOn(t, cells, "Language")
	if r == nil || r.Value != "English" {
		t.Errorf("Read(Language) = %+v, want English from the same cell", r)
	}
}

func TestReadRestOfLabelNeedsTheLabelFirstAndASeparator(t *testing.T) {
	s := android(
		nd(1, textView, rect(40, 200, 1040, 260), text("Your total is shown below")),
		nd(1, textView, rect(40, 300, 1040, 360), text("Order total")),
		nd(1, textView, rect(40, 400, 1040, 460), text("Total (incl. tax)")),
		nd(1, textView, rect(40, 500, 1040, 560), text("Wi-Fi off")),
		nd(1, textView, rect(40, 600, 1040, 660), text("3 items in Basket")),
		nd(1, textView, rect(40, 700, 1040, 760), text("Language, English")),
		nd(1, textView, rect(40, 800, 1040, 860), text("Status - shipped")),
		nd(1, textView, rect(40, 900, 1040, 960), text("Speed = fast")),
	)
	expectNoValue(t, s, "Total", "tax", "off", "Wi-Fi", "Basket", "items")
	expectReading(t, s, "Language", "English", "label", 1)
	expectReading(t, s, "Status", "shipped", "label", 1)
	expectReading(t, s, "speed", "fast", "label", 1)
}

func TestReadExactTotalAmongLongerOnes(t *testing.T) {
	s := android(
		nd(1, textView, rect(40, 200, 1040, 260), text("Your total is shown below")),
		nd(1, textView, rect(40, 300, 500, 360), text("Subtotal")),
		nd(1, textView, rect(800, 300, 1040, 360), text("$20")),
		nd(1, textView, rect(40, 400, 500, 460), text("Total")),
		nd(1, textView, rect(800, 400, 1040, 460), text("$24")),
		nd(1, textView, rect(40, 500, 500, 560), text("Total (incl. tax)")),
		nd(1, textView, rect(800, 500, 1040, 560), text("$30")),
	)
	expectReading(t, s, "Total", "$24", "right", 1)
	expectReading(t, s, "total (incl. tax)", "$30", "right", 1)
	expectReading(t, s, "Subtotal", "$20", "right", 1)
}

func TestReadMatchesCountsEveryMatchWhicheverRuleWon(t *testing.T) {
	s := android(
		nd(1, textView, rect(40, 300, 500, 360), text("Price")),
		nd(1, "android.widget.Button", rect(800, 300, 1040, 360), text("Edit"), click),
		nd(1, textView, rect(40, 500, 500, 560), text("Price")),
		nd(1, textView, rect(800, 500, 1040, 560), text("$2")),
		nd(1, "android.widget.Switch", rect(40, 700, 1040, 760), text("Price"), click, checked),
		nd(1, textView, rect(40, 900, 1040, 960), text("Price: $9")),
	)
	expectReading(t, s, "Price", "on", "switch", 4)
}

func TestReadSaysWhatKindOfElementAPositionalValueIs(t *testing.T) {
	s := android(
		nd(1, textView, rect(40, 300, 500, 360), text("Total")),
		nd(1, textView, rect(800, 300, 1040, 360), text("$42")),
		nd(1, textView, rect(40, 500, 500, 560), text("Dark theme")),
		nd(1, "android.widget.Switch", rect(900, 500, 1040, 560), click, checked),
		nd(1, "android.widget.Switch", rect(40, 700, 1040, 760), text("Sync"), click, checked),
	)
	for q, want := range map[string]string{"Total": "Text", "Dark theme": "Switch", "Sync": ""} {
		if r, _ := readOn(t, s, q); r == nil || r.Of != want {
			t.Errorf("Read(%q) = %+v, want its value from a %q", q, r, want)
		}
	}
}
