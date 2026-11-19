package web

import (
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// Controls as page.js reports them: a checkbox, a radio or a switch is named by its label,
// not by its value ("on"), so its label wrapper and it are one thing; a range input is a
// slider with its value; aria-pressed and aria-selected are selected; aria-checked is checked.
const controlsPage = `<!doctype html><html><body style="margin:0">
<label id="milk_row"><input type="checkbox" id="check_milk">Milk</label>
<input type="checkbox" id="check_eggs"><label for="check_eggs">Eggs</label>
<label><input type="radio" name="c" id="radio_red" value="red" checked>Red</label>
<label><input type="checkbox" role="switch" id="switch_wifi">Wi-Fi</label>
<input type="checkbox" id="check_bare">
<input type="range" id="slider_volume" min="0" max="100" value="50" aria-label="Volume">
<label>Speed <input type="range" id="slider_speed" value="20"></label>
<button id="seg_m" aria-pressed="true">M</button>
<button id="seg_s" aria-pressed="false">S</button>
<div id="tab_two" role="tab" aria-selected="true">Two</div>
<div id="aria_switch" role="switch" aria-checked="true" tabindex="0">Dark mode</div>
<div id="aria_check" role="checkbox" aria-checked="false" tabindex="0">Remember me</div>
<div id="menu_check" role="menuitemcheckbox" aria-checked="true" tabindex="0">Show grid</div>
</body></html>`

func TestPageJSControls(t *testing.T) {
	d, ctx := openPage(t, controlsPage)
	s := screenWith(t, d, ctx, "menu_check")
	get := func(id string) *ui.Node {
		n := find(s, id)
		if n == nil {
			t.Fatalf("#%s not listed", id)
		}
		return n
	}
	for id, label := range map[string]string{"check_milk": "Milk", "check_eggs": "Eggs", "radio_red": "Red", "switch_wifi": "Wi-Fi"} {
		if n := get(id); n.Text != label || !n.Checkable {
			t.Errorf("#%s: text %q, want its label %q (not its value): %+v", id, n.Text, label, n)
		}
	}
	if n := get("radio_red"); !n.Checked {
		t.Errorf("checked radio: %+v", n)
	}
	if n := get("check_bare"); n.Text != "" {
		t.Errorf("a checkbox with no label has no text, not its value: %q", n.Text)
	}
	if n := get("slider_volume"); n.Text != "Volume" || n.Value != "50" || n.Class != "input:range[slider]" || n.Editable {
		t.Errorf("range input: a slider named by its label, with its value: %+v", n)
	}
	if n := get("slider_speed"); n.Text != "Speed" || n.Value != "20" {
		t.Errorf("range input in a label: %+v", n)
	}
	if !get("seg_m").Selected || get("seg_s").Selected {
		t.Error("aria-pressed=true is selected, false is not")
	}
	if !get("tab_two").Selected {
		t.Error("aria-selected=true is selected")
	}
	if n := get("aria_switch"); !n.Checkable || !n.Checked {
		t.Errorf("role=switch aria-checked=true: %+v", n)
	}
	if n := get("aria_check"); !n.Checkable || n.Checked {
		t.Errorf("role=checkbox aria-checked=false: %+v", n)
	}
	if n := get("menu_check"); !n.Checkable || !n.Checked {
		t.Errorf("role=menuitemcheckbox aria-checked=true: %+v", n)
	}
}

// With a modal dialog open, its root is marked Modal and the page under it UnderModal (an
// element that holds the dialog, as the body, is neither); with none open, nothing is.
const modalPage = `<!doctype html><html><body style="margin:0">
<div id="page">
  <h1 id="title">Shop</h1>
  <button id="buy">Buy</button>
  <div id="sheet" role="dialog" aria-modal="true" style="position:fixed;left:100px;top:100px;width:300px;height:200px;background:#fff">
    <p id="ask">Sure?</p>
    <button id="yes">Yes</button>
  </div>
</div>
<dialog id="native"><button id="native_ok">OK</button></dialog>
<button id="open_native" onclick="document.getElementById('sheet').remove(); document.getElementById('native').showModal()">Open</button>
</body></html>`

func TestPageJSModal(t *testing.T) {
	d, ctx := openPage(t, modalPage)
	s := screenWith(t, d, ctx, "yes")
	check := func(s *ui.Screen, id string, modal, under bool) {
		t.Helper()
		n := find(s, id)
		if n == nil {
			t.Fatalf("#%s not listed", id)
		}
		if n.Modal != modal || n.UnderModal != under {
			t.Errorf("#%s: Modal %v UnderModal %v, want %v %v", id, n.Modal, n.UnderModal, modal, under)
		}
	}
	check(s, "sheet", true, false)
	check(s, "yes", false, false)
	check(s, "ask", false, false)
	check(s, "page", false, false) // holds the dialog
	check(s, "title", false, true)
	check(s, "buy", false, true)
	check(s, "open_native", false, true)

	// A <dialog> shown modal: the same, and the aria-modal one is gone.
	if err := d.eval(ctx, "document.getElementById('open_native').click()", nil); err != nil {
		t.Fatal(err)
	}
	s = screenWith(t, d, ctx, "native_ok")
	check(s, "native", true, false)
	check(s, "native_ok", false, false)
	check(s, "buy", false, true)
	check(s, "title", false, true)

	if err := d.eval(ctx, "document.getElementById('native').close()", nil); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); find(s, "native_ok") != nil && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
		var err error
		if s, err = d.Screen(ctx); err != nil {
			t.Fatal(err)
		}
	}
	check(s, "buy", false, false)
	check(s, "title", false, false)
}

// Review of look-web-shot-speed: an aria-modal drawer parked off screen is no open dialog; a
// <dialog> shown modal in a frame blocks the frame, not the top page; and a toast or a popover
// drawn above the open dialog is not under it.
const modalEdgesPage = `<!doctype html><html><body style="margin:0">
<button id="buy">Buy</button>
<nav id="drawer" role="dialog" aria-modal="true" style="position:fixed;left:-300px;top:0;width:250px;height:100%">Menu</nav>
<iframe id="frame" style="border:0;width:300px;height:150px" srcdoc="<dialog id='fd'><button id='fd_ok'>Framed OK</button></dialog><script>document.getElementById('fd').showModal()</script>"></iframe>
<script>
function openDialog() {
  const d = document.createElement('div');
  d.id = 'sheet'; d.setAttribute('role', 'dialog'); d.setAttribute('aria-modal', 'true');
  d.style.cssText = 'position:fixed;left:100px;top:100px;width:300px;height:200px;background:#fff;z-index:10';
  d.innerHTML = '<button id="yes">Yes</button>';
  const toast = document.createElement('div');
  toast.id = 'toast'; toast.textContent = 'Saved';
  toast.style.cssText = 'position:fixed;left:0;bottom:0;width:200px;height:40px;background:#333;color:#fff;z-index:1000';
  const pop = document.createElement('div');
  pop.id = 'tip'; pop.setAttribute('popover', 'manual'); pop.textContent = 'A tip';
  pop.style.cssText = 'position:fixed;inset:auto;right:0;top:0;width:120px;height:40px;margin:0';
  // The same z-index as the dialog: drawn above it when later in the page, under it before.
  const early = document.createElement('div');
  early.id = 'early'; early.textContent = 'Early banner';
  early.style.cssText = 'position:fixed;left:0;top:60px;width:200px;height:30px;z-index:10';
  const late = document.createElement('div');
  late.id = 'late'; late.textContent = 'Late banner';
  late.style.cssText = 'position:fixed;left:0;top:400px;width:200px;height:30px;z-index:10';
  document.body.prepend(early);
  document.body.append(d, toast, pop, late);
  pop.showPopover();
}
// A popover opened before a <dialog> shown modal sits under it in the top layer: the page
// cannot tell that order, so the popover is left unmarked.
function openNative() {
  document.getElementById('sheet').remove();
  const n = document.createElement('dialog');
  n.id = 'native'; n.innerHTML = '<button id="native_ok">OK</button>';
  document.body.append(n);
  n.showModal();
}
</script>
</body></html>`

func TestPageJSModalEdges(t *testing.T) {
	d, ctx := openPage(t, modalEdgesPage)
	s := screenWith(t, d, ctx, "fd_ok")
	for _, n := range s.Nodes {
		if n.Modal || n.UnderModal {
			t.Errorf("no dialog open on the top page, yet %s #%s %q: Modal %v UnderModal %v", n.Class, n.ID, n.Text, n.Modal, n.UnderModal)
		}
	}
	if err := d.eval(ctx, "openDialog()", nil); err != nil {
		t.Fatal(err)
	}
	s = screenWith(t, d, ctx, "tip")
	want := map[string][2]bool{"sheet": {true, false}, "yes": {false, false}, "buy": {false, true},
		"toast": {false, false}, "tip": {false, false}, "drawer": {false, true},
		"early": {false, true}, "late": {false, false}}
	checkAll := func(want map[string][2]bool) {
		t.Helper()
		for id, w := range want {
			n := find(s, id)
			if n == nil {
				t.Errorf("#%s not listed", id)
				continue
			}
			if n.Modal != w[0] || n.UnderModal != w[1] {
				t.Errorf("#%s: Modal %v UnderModal %v, want %v %v", id, n.Modal, n.UnderModal, w[0], w[1])
			}
		}
	}
	checkAll(want)
	if err := d.eval(ctx, "openNative()", nil); err != nil {
		t.Fatal(err)
	}
	s = screenWith(t, d, ctx, "native_ok")
	checkAll(map[string][2]bool{"native": {true, false}, "tip": {false, false}, "buy": {false, true},
		"toast": {false, true}, "late": {false, true}})
}
