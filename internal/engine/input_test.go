package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// inputText types the evaluated text, waiting 300 ms for the field to react.
func TestInputText(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- inputText: hello\n- inputText: \"${1+1}-x\"\n- inputText: {text: map form}"))
	if want := []string{"hello", "2-x", "map form"}; !reflect.DeepEqual(f.typed, want) {
		t.Errorf("typed %q, want %q", f.typed, want)
	}
	if f.typeOpts[0].NoChangeMs != 300 {
		t.Errorf("type options %+v", f.typeOpts[0])
	}
}

// Typing that the device fails fails the command.
func TestInputTextError(t *testing.T) {
	f := &fakeDriver{screen: static()}
	f.fail("TypeText", errors.New("no focused field"))
	mustFail(t, runWith(t, f, quick, "- inputText: hello"), "no focused field")
}

// The inputRandom* commands type generated values of the asked kind and length.
func TestInputRandom(t *testing.T) {
	digits := func(s string) bool { return strings.IndexFunc(s, func(r rune) bool { return !unicode.IsDigit(r) }) < 0 }
	letters := func(s string) bool { return strings.IndexFunc(s, func(r rune) bool { return r < 'a' || r > 'z' }) < 0 }
	for _, tc := range []struct {
		cmd string
		ok  func(string) bool
	}{
		{"inputRandomText", func(s string) bool { return len(s) == 8 && letters(s) }},
		{"inputRandomText: {length: 5}", func(s string) bool { return len(s) == 5 && letters(s) }},
		{"inputRandomText: {length: 0}", func(s string) bool { return len(s) == 8 }}, // non-positive: the default
		{"inputRandomNumber: {length: 4}", func(s string) bool { return len(s) == 4 && digits(s) && s[0] != '0' }},
		{"inputRandomNumber", func(s string) bool { return len(s) == 8 && digits(s) }},
		{"inputRandomEmail", func(s string) bool { return strings.Count(s, "@") == 1 && strings.Contains(s, ".") }},
		{"inputRandomPersonName", func(s string) bool { return strings.Count(s, " ") >= 1 }},
		{"inputRandomCityName", func(s string) bool { return s != "" }},
		{"inputRandomCountryName", func(s string) bool { return s != "" }},
		{"inputRandomColorName", func(s string) bool { return s != "" && letters(s) }},
	} {
		t.Run(tc.cmd, func(t *testing.T) {
			f := &fakeDriver{screen: static()}
			mustPass(t, runWith(t, f, quick, "- "+tc.cmd))
			if len(f.typed) != 1 || !tc.ok(f.typed[0]) {
				t.Errorf("typed %q", f.typed)
			}
		})
	}
}

// eraseText erases 50 characters unless told how many.
func TestEraseText(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- eraseText\n- eraseText: 5\n- eraseText: {charactersToErase: 3}"))
	if want := []int{50, 5, 3}; !reflect.DeepEqual(f.erased, want) {
		t.Errorf("erased %v, want %v", f.erased, want)
	}
	f2 := &fakeDriver{screen: static()}
	f2.fail("EraseText", errors.New("no field"))
	mustFail(t, runWith(t, f2, quick, "- eraseText: 2"), "no field")
}

// copyTextFrom copies an element's text (else hint, description, error); pasteText types it.
func TestCopyAndPaste(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    *ui.Node
		want string
	}{
		{"text", &ui.Node{Text: "Hello", Hint: "h", Desc: "d"}, "Hello"},
		{"hint", &ui.Node{Hint: "Email", Desc: "d"}, "Email"},
		{"desc", &ui.Node{Desc: "Avatar"}, "Avatar"},
		{"error", &ui.Node{Error: "Required"}, "Required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := tc.n
			n.ID, n.Bounds, n.Enabled = "src", ui.Rect{R: 400, B: 50}, true
			f := &fakeDriver{screen: static(n)}
			mustPass(t, runWith(t, f, quick, "- copyTextFrom: {id: src}\n- pasteText\n- inputText: ${maestro.copiedText}/${tb.copiedText}"))
			if want := []string{tc.want, tc.want + "/" + tc.want}; !reflect.DeepEqual(f.typed, want) {
				t.Errorf("typed %q, want %q", f.typed, want)
			}
		})
	}
}

// An element with nothing to copy fails copyTextFrom.
func TestCopyTextFromEmpty(t *testing.T) {
	n := &ui.Node{ID: "src", Bounds: ui.Rect{R: 400, B: 50}}
	f := &fakeDriver{screen: static(n)}
	mustFail(t, runWith(t, f, quick, "- copyTextFrom: {id: src}"), "it has no text")
}

// copyTextFrom an element that is not there: an optional one warns.
func TestCopyTextFromMissing(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static()}
	res := runWith(t, f, quick, "- copyTextFrom: {id: nope, optional: true}\n- pasteText")
	mustPass(t, res)
	if got := stepStatuses(res); !reflect.DeepEqual(got, []Status{Warned, Passed}) {
		t.Errorf("statuses %v", got)
	}
	if len(f.typed) != 0 {
		t.Errorf("pasted %q with nothing copied", f.typed)
	}
}

// setClipboard sets what pasteText types; pasteText with nothing copied types nothing.
func TestSetClipboard(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- pasteText\n- setClipboard: \"${'a' + 'b'}\"\n- pasteText\n- setClipboard: {text: c}\n- pasteText"))
	if want := []string{"ab", "c"}; !reflect.DeepEqual(f.typed, want) {
		t.Errorf("typed %q, want %q", f.typed, want)
	}
}

// pressKey sends the key and returns once the UI starts reacting; if it changed, the next
// command waits for it to settle.
func TestPressKey(t *testing.T) {
	for _, tc := range []struct {
		res      driver.SettleResult
		deferred bool
	}{{driver.Changed, true}, {driver.NoChange, false}, {"", false}} {
		t.Run(string(tc.res), func(t *testing.T) {
			f := &fakeDriver{screen: static(), keySettle: tc.res}
			mustPass(t, runWith(t, f, quick, "- pressKey: Enter\n- pressKey: {key: Home}\n- eraseText: 1"))
			if want := []string{"Enter", "Home"}; !reflect.DeepEqual(f.keys, want) {
				t.Errorf("keys %q", f.keys)
			}
			if !f.keyOpts[0].React {
				t.Errorf("key options %+v: want React", f.keyOpts[0])
			}
			if got := len(f.waitDur) > 0; got != tc.deferred {
				t.Errorf("settle waits %v, want deferred=%v", f.waitDur, tc.deferred)
			}
		})
	}
}

// A key the device rejects fails on Android; on iOS keys it lacks do nothing.
func TestPressKeyError(t *testing.T) {
	f := &fakeDriver{screen: static()}
	f.fail("PressKey", errors.New("unknown key"))
	mustFail(t, runWith(t, f, quick, "- pressKey: Volume Up"), "unknown key")
	f2 := &fakeDriver{screen: static(), plat: "ios"}
	f2.fail("PressKey", errors.New("unknown key"))
	mustPass(t, runWith(t, f2, quick, "- pressKey: Volume Up"))
}

// back presses Android's back key; on iOS it does nothing.
func TestBack(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- back\n- action: back"))
	if want := []string{"back", "back"}; !reflect.DeepEqual(f.keys, want) {
		t.Errorf("keys %q", f.keys)
	}
	f2 := &fakeDriver{screen: static(), plat: "ios"}
	f2.fail("PressKey", errors.New("must not be called"))
	mustPass(t, runWith(t, f2, quick, "- back"))
	if len(f2.keys) != 0 {
		t.Errorf("iOS back pressed %q", f2.keys)
	}
	f3 := &fakeDriver{screen: static()}
	f3.fail("PressKey", errors.New("agent gone"))
	mustFail(t, runWith(t, f3, quick, "- back"), "agent gone")
}

// hideKeyboard asks the device to hide it and checks it went; a keyboard that stays fails.
func TestHideKeyboard(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- hideKeyboard\n- hide keyboard"))
	if n := len(f.called("HideKeyboard")); n != 2 {
		t.Errorf("%d HideKeyboard calls", n)
	}
	f2 := &fakeDriver{screen: static(), kbShown: true}
	mustFail(t, runWith(t, f2, quick, "- hideKeyboard"), "Couldn't hide the keyboard")
	f3 := &fakeDriver{screen: static()}
	f3.fail("HideKeyboard", errors.New("no ime"))
	mustFail(t, runWith(t, f3, quick, "- hideKeyboard"), "no ime")
	f4 := &fakeDriver{screen: static()}
	f4.fail("Screen", errors.New("read failed"))
	mustFail(t, runWith(t, f4, quick, "- hideKeyboard"), "read failed")
}

type checkedTypingDriver struct {
	*fakeDriver
	checked   []string
	verifyErr error
}

func (d *checkedTypingDriver) TypeTextChecked(_ context.Context, text string, _ driver.ActionOpts) (driver.ActionResult, error) {
	d.checked = append(d.checked, text)
	return driver.ActionResult{}, d.verifyErr
}

func TestUnavailableDeviceFocusKeepsTheScreenGuard(t *testing.T) {
	f := &fakeDriver{}
	text := ""
	f.screen = func() []*ui.Node {
		return []*ui.Node{{Class: "EditText", ID: "custom", Text: text, Editable: true, Focused: true}}
	}
	dropped := false
	f.onType = func(s string) {
		if !dropped {
			dropped = true
		} else {
			text += s
		}
	}
	d := &checkedTypingDriver{fakeDriver: f, verifyErr: driver.ErrTypingVerificationUnavailable}
	mustPass(t, runWith(t, d, quick, "- inputText: auto"))
	if text != "auto" || strings.Join(f.typed, "|") != "a|a|uto" {
		t.Fatalf("fallback text %q, injected %v", text, f.typed)
	}
}

func TestDeviceCheckedTypingAvoidsHierarchyReads(t *testing.T) {
	f := &fakeDriver{}
	f.screen = func() []*ui.Node { t.Fatal("device-verified typing must not read the whole screen"); return nil }
	d := &checkedTypingDriver{fakeDriver: f}
	mustPass(t, runWith(t, d, quick, "- inputText: auto"))
	if got := strings.Join(d.checked, "|"); got != "auto" {
		t.Fatalf("checked typing %q", got)
	}
	if len(f.typed) != 0 {
		t.Fatal("runner also used the unchecked typing path")
	}
}

// Drivers without device-side verification retain the runner's first-key retry.
func TestTypeFirstKeyChecked(t *testing.T) {
	t.Parallel()
	for _, drop := range []bool{true, false} {
		f := &fakeDriver{}
		var mu sync.Mutex
		text, dropped := "", false
		f.screen = func() []*ui.Node {
			mu.Lock()
			defer mu.Unlock()
			n := &ui.Node{Class: "EditText", ID: "field", Text: text, Editable: true, Focused: true, Enabled: true, Bounds: ui.Rect{T: 100, R: 400, B: 160}}
			return []*ui.Node{n}
		}
		f.onType = func(s string) {
			mu.Lock()
			if drop && !dropped {
				dropped = true // the first key is lost
			} else {
				text += s
			}
			mu.Unlock()
			f.change(true)
		}
		mustPass(t, runWith(t, f, quick, "- inputText: auto"))
		f.mu.Lock()
		typed := strings.Join(f.typed, "|")
		f.mu.Unlock()
		want := "a|uto"
		if drop {
			want = "a|a|uto"
		}
		if typed != want || text != "auto" {
			t.Errorf("drop %v: typed %q (want %q), field %q", drop, typed, want, text)
		}
	}
}

// A one-digit code box moves focus to the next box as a key lands: the first key counts as
// landed (focus moved), and is not typed again.
func TestTypeFirstKeyFocusMoves(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var mu sync.Mutex
	boxes := make([]string, 6)
	focus := 0
	f.screen = func() []*ui.Node {
		mu.Lock()
		defer mu.Unlock()
		var ns []*ui.Node
		for i := range boxes {
			ns = append(ns, &ui.Node{Class: "EditText", ID: fmt.Sprintf("otp_%d", i+1), Text: boxes[i], Editable: true,
				Focused: i == focus, Enabled: true, Bounds: ui.Rect{L: i * 60, T: 100, R: i*60 + 50, B: 160}})
		}
		return ns
	}
	f.onType = func(s string) {
		mu.Lock()
		for _, ch := range s {
			if focus < len(boxes) {
				boxes[focus] = string(ch)
				focus++
			}
		}
		mu.Unlock()
		f.change(true)
	}
	mustPass(t, runWith(t, f, quick, `- inputText: "123456"`))
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(boxes, ""); got != "123456" {
		t.Errorf("boxes %q, want 123456 (the first key typed once)", got)
	}
}

// A WebView/custom provider may send no event for the rest of the text. The
// first-key verification snapshot must not remain current after typing it.
func TestTypingRemainderInvalidatesFirstKeySnapshotWithoutEvents(t *testing.T) {
	f := &fakeDriver{}
	var mu sync.Mutex
	text := ""
	f.screen = func() []*ui.Node {
		mu.Lock()
		defer mu.Unlock()
		return []*ui.Node{{Class: "EditText", ID: "field", Text: text, Editable: true,
			Focused: true, Enabled: true, Bounds: ui.Rect{R: 400, B: 80}}}
	}
	f.onType = func(s string) { mu.Lock(); text += s; mu.Unlock() }
	mustPass(t, runWith(t, f, quick, "- inputText: hi\n- assertVisible: {id: field, text: hi}"))
}

// AND-004: a transparent one-touch catcher can swallow the reference-field
// tap while the amount field retains focus. Never send reference text there.
func TestInputRefocusesRequestedFieldBeforeTyping(t *testing.T) {
	for _, canFocus := range []bool{true, false} {
		t.Run(fmt.Sprintf("canFocus=%v", canFocus), func(t *testing.T) {
			amount, reference := "40", ""
			focusedReference := false
			touches := 0
			f := &fakeDriver{tapSettle: driver.NoChange}
			f.screen = func() []*ui.Node {
				a, b := btn(amount, "amount", 100), btn(reference, "reference", 300)
				a.Editable, b.Editable = true, true
				a.Focused, b.Focused = !focusedReference, focusedReference
				return []*ui.Node{a, b}
			}
			f.onTap = func(_, _ int) {
				touches++
				if touches > 1 && canFocus {
					focusedReference = true
					f.change(true)
				}
			}
			f.onType = func(text string) {
				if focusedReference {
					reference += text
				} else {
					amount += text
				}
				f.change(true)
			}
			res := runWith(t, f, quick, `
- tapOn: {id: reference}
- inputText: rent
- assertVisible: {id: amount, text: '40'}
- assertVisible: {id: reference, text: rent}
`)
			if canFocus {
				mustPass(t, res)
				if reference != "rent" || amount != "40" || touches != 2 {
					t.Fatalf("amount=%q reference=%q touches=%d", amount, reference, touches)
				}
			} else {
				if res.Err == nil || len(f.typed) != 0 || touches > 2 {
					t.Fatalf("typed into wrong field: amount=%q keys=%v touches=%d error=%v", amount, f.typed, touches, res.Err)
				}
			}
		})
	}
}
