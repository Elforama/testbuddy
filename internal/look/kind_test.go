package look

import (
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// Android kinds come from the class (full or short name) plus the clickable/checkable flags.
func TestKindAndroid(t *testing.T) {
	cases := []struct {
		class string
		opts  []opt
		want  string
	}{
		{"android.widget.EditText", nil, "Field"},
		{"android.widget.AutoCompleteTextView", nil, "Field"},
		{"android.view.View", []opt{editable}, "Field"},
		{"android.widget.EditText", []opt{password}, "SecureField"},
		{"android.view.View", []opt{password}, "SecureField"},
		{"android.widget.TextView", nil, "Text"},
		{"android.widget.TextView", []opt{click}, "Button"},
		{"android.widget.TextView", []opt{longClick}, "Button"},
		{"android.widget.Button", nil, "Button"},
		{"android.widget.ImageButton", nil, "Button"},
		{"com.google.android.material.button.MaterialButton", nil, "Button"},
		{"androidx.appcompat.widget.AppCompatButton", nil, "Button"},
		{"android.widget.CheckBox", nil, "CheckBox"},
		{"androidx.appcompat.widget.AppCompatCheckBox", nil, "CheckBox"},
		{"com.google.android.material.checkbox.MaterialCheckBox", nil, "CheckBox"},
		{"android.widget.Switch", nil, "Switch"},
		{"androidx.appcompat.widget.SwitchCompat", nil, "Switch"},
		{"com.google.android.material.switchmaterial.SwitchMaterial", nil, "Switch"},
		{"android.widget.RadioButton", nil, "Radio"},
		{"androidx.appcompat.widget.AppCompatRadioButton", nil, "Radio"},
		{"android.widget.ImageView", nil, "Image"},
		{"android.widget.ImageView", []opt{click}, "Button"},
		{"android.widget.ImageView", []opt{checkable}, "Button"},
		{"android.widget.SeekBar", nil, "Slider"},
		{"android.webkit.WebView", nil, "Web"},
		{"android.widget.LinearLayout", nil, "View"},
		{"android.widget.LinearLayout", []opt{click}, "Button"},
		{"android.widget.LinearLayout", []opt{longClick}, "Button"},
		{"android.widget.FrameLayout", []opt{checkable}, "Button"},
		{"androidx.recyclerview.widget.RecyclerView", []opt{scrollable}, "View"},
		{"", nil, "View"},
	}
	for _, c := range cases {
		n := nd(1, c.class, rect(0, 0, 10, 10), c.opts...)
		if got := kind(n, "android"); got != c.want {
			t.Errorf("kind(%s %+v) = %s, want %s", c.class, flags(n), got, c.want)
		}
	}
}

func flags(n *ui.Node) []string {
	var f []string
	for name, on := range map[string]bool{"click": n.Clickable, "long": n.LongClickable, "checkable": n.Checkable, "editable": n.Editable, "password": n.Password} {
		if on {
			f = append(f, name)
		}
	}
	return f
}

// iOS kinds come from the element type; only control types are actionable (the clickable
// flag is ignored), so static text is never a Button.
func TestKindIOS(t *testing.T) {
	cases := []struct {
		class string
		opts  []opt
		want  string
	}{
		{"TextField", nil, "Field"},
		{"SearchField", nil, "Field"},
		{"SecureTextField", nil, "SecureField"},
		{"TextView", []opt{editable}, "Field"},
		{"StaticText", nil, "Text"},
		{"StaticText", []opt{click}, "Text"},
		{"Text", nil, "Text"},
		{"Button", nil, "Button"},
		{"Cell", nil, "Cell"},
		{"Link", nil, "Link"},
		{"Switch", nil, "Switch"},
		{"Toggle", nil, "Switch"},
		{"CheckBox", nil, "CheckBox"},
		{"Slider", nil, "Slider"},
		{"Image", nil, "Image"},
		{"Icon", nil, "Image"},
		{"Tab", nil, "Tab"},
		{"TabBar", nil, "TabBar"},
		{"Stepper", nil, "Button"},
		{"SegmentedControl", nil, "Button"},
		{"MenuItem", nil, "Button"},
		{"PickerWheel", nil, "Button"},
		{"Other", nil, "View"},
		{"Other", []opt{click}, "View"},
		{"NavigationBar", nil, "View"},
		{"WebView", nil, "Web"},
	}
	for _, c := range cases {
		n := nd(1, c.class, rect(0, 0, 10, 10), c.opts...)
		if got := kind(n, "ios"); got != c.want {
			t.Errorf("kind(%s %v) = %s, want %s", c.class, flags(n), got, c.want)
		}
	}
}

// Web kinds come from the ARIA role first, then the tag (and input type), then the flags.
func TestWebKind(t *testing.T) {
	cases := []struct {
		class string
		opts  []opt
		want  string
	}{
		// roles win over tags
		{"div[button]", nil, "Button"},
		{"a[button]", nil, "Button"},
		{"li[menuitem]", nil, "Button"},
		{"div[tab]", nil, "Button"},
		{"span[link]", nil, "Link"},
		{"div[checkbox]", nil, "CheckBox"},
		{"button[switch]", nil, "Switch"},
		{"div[radio]", nil, "Radio"},
		{"div[heading]", nil, "Heading"},
		{"div[navigation]", nil, "View"}, // unknown role: falls through to the tag
		{"p[presentation]", nil, "Text"},
		// tags
		{"a", nil, "Link"},
		{"button", nil, "Button"},
		{"summary", nil, "Button"},
		{"input", nil, "Field"},
		{"input:text", nil, "Field"},
		{"input:email", nil, "Field"},
		{"input:search", nil, "Field"},
		{"input:checkbox", nil, "CheckBox"},
		{"input:radio", nil, "Radio"},
		{"input:submit", nil, "Button"},
		{"input:button", nil, "Button"},
		{"input:reset", nil, "Button"},
		{"input:image", nil, "Button"},
		{"input:password", nil, "SecureField"},
		{"input:checkbox[switch]", nil, "Switch"},
		{"textarea", nil, "Field"},
		{"select", nil, "Select"},
		{"h1", nil, "Heading"},
		{"h6", nil, "Heading"},
		{"h7", nil, "View"},
		// text tags
		{"p", nil, "Text"},
		{"span", nil, "Text"},
		{"label", nil, "Text"},
		{"li", nil, "Text"},
		{"td", nil, "Text"},
		{"code", nil, "Text"},
		{"span", []opt{click}, "Button"},
		{"li", []opt{click}, "Button"},
		// other tags fall back to the flags
		{"div", []opt{editable}, "Field"},
		{"div", []opt{click}, "Button"},
		{"div", []opt{text("Hello")}, "Text"},
		{"div", nil, "View"},
		{"img", []opt{desc("Logo")}, "View"},
		{"img", []opt{click}, "Button"},
		{"", nil, "View"},
	}
	for _, c := range cases {
		n := nd(1, c.class, rect(0, 0, 10, 10), c.opts...)
		if got := webKind(n); got != c.want {
			t.Errorf("webKind(%q %v) = %s, want %s", c.class, flags(n), got, c.want)
		}
		if got := kind(n, "web"); got != c.want {
			t.Errorf("kind(%q, web) = %s, want %s", c.class, got, c.want)
		}
	}
}

// On the web only the clickable flag makes an element actionable.
func TestActionable(t *testing.T) {
	cases := []struct {
		platform, class string
		opts            []opt
		want            bool
	}{
		{"web", "button", nil, false},
		{"web", "div", []opt{click}, true},
		{"web", "div", []opt{longClick, checkable}, false},
		{"ios", "Button", nil, true},
		{"ios", "Other", []opt{click}, false},
		{"android", "android.view.View", []opt{click}, true},
		{"android", "android.view.View", []opt{longClick}, true},
		{"android", "android.view.View", []opt{checkable}, true},
		{"android", "android.widget.Button", nil, false},
	}
	for _, c := range cases {
		n := nd(1, c.class, rect(0, 0, 10, 10), c.opts...)
		if got := actionable(n, c.platform); got != c.want {
			t.Errorf("actionable(%s %s %v) = %v, want %v", c.platform, c.class, flags(n), got, c.want)
		}
	}
}

// Small helpers: ids worth showing, Android id shortening, clipping, letters/digits.
func TestHelpers(t *testing.T) {
	for id, want := range map[string]bool{
		"":                     false,
		"com.app:id/title":     false,
		"android:id/content":   false,
		"login_button":         true,
		"LoginButton":          true,
		"settings/wifi":        true,
		"com.app.debug:id/x_y": false,
	} {
		if got := usefulID(id); got != want {
			t.Errorf("usefulID(%q) = %v, want %v", id, got, want)
		}
	}
	for id, want := range map[string]string{
		"com.app:id/title":   "title",
		"android:id/content": "content",
		"login_button":       "login_button",
		"":                   "",
		"a:id/b:id/c":        "b:id/c",
	} {
		if got := ShortID(id); got != want {
			t.Errorf("ShortID(%q) = %q, want %q", id, got, want)
		}
	}
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"  spaced \n\t out  ", 20, "spaced out"},
		{"exactly10!", 10, "exactly10!"},
		{"eleven char", 10, "eleven ch…"},
		{"ééééééééééé", 5, "éééé…"},
		{"", 5, ""},
	} {
		if got := clip(c.in, c.n); got != c.want {
			t.Errorf("clip(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
	for s, want := range map[string]bool{"": false, "›": false, "": false, "...": false, "a": true, "7": true, "é": true, "日本": true, "★ 4": true} {
		if got := hasLetterOrDigit(s); got != want {
			t.Errorf("hasLetterOrDigit(%q) = %v, want %v", s, got, want)
		}
	}
}

// A bar window is a thin non-app window along the top or bottom edge (status and
// navigation bars); anything else is not.
func TestBarWindow(t *testing.T) {
	s := &ui.Screen{Width: phoneW, Height: phoneH}
	cases := []struct {
		name string
		w    ui.Window
		want bool
	}{
		{"status bar", ui.Window{Type: "system", Bounds: rect(0, 0, 1080, 63)}, true},
		{"navigation bar", ui.Window{Type: "system", Bounds: rect(0, 2337, 1080, 2400)}, true},
		{"bar overlapping the top", ui.Window{Type: "system", Bounds: rect(0, -10, 1080, 50)}, true},
		{"thin toast in the middle", ui.Window{Type: "system", Bounds: rect(100, 1800, 980, 1900)}, false},
		{"tall system dialog", ui.Window{Type: "system", Bounds: rect(0, 0, 1080, 1200)}, false},
		{"just too tall for a bar", ui.Window{Type: "system", Bounds: rect(0, 0, 1080, 240)}, false},
		{"thin app window", ui.Window{Type: "app", Bounds: rect(0, 0, 1080, 63)}, false},
		{"keyboard", ui.Window{Type: "keyboard", Bounds: rect(0, 2337, 1080, 2400)}, false},
		{"overlay bar", ui.Window{Type: "overlay", Bounds: rect(0, 0, 1080, 63)}, true},
		{"no bounds", ui.Window{Type: "system"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := barWindow(&c.w, s); got != c.want {
				t.Errorf("barWindow = %v, want %v", got, c.want)
			}
		})
	}
	if barWindow(&ui.Window{Type: "system", Bounds: rect(0, 0, 10, 1)}, &ui.Screen{}) {
		t.Error("with no screen height nothing is a bar")
	}
}
