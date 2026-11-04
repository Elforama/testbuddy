package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// The web agent (page.js) in a real browser: what its snapshot lists, and what it leaves out.
// It needs Chrome, so it runs only with TB_BROWSER_TESTS=1 (CI's web agent job sets it).
const testPage = `<!doctype html><html><body>
<h1>Shop</h1>
<button id="buy">Buy</button>
<button id="gone" style="display:none">Hidden button</button>
<div id="ghost" style="visibility:hidden">Invisible text</div>
<div id="banner" style="height:0;overflow:hidden">Banner text</div>
<input id="email" placeholder="Email">
<div id="host"></div>
<script>
  const root = document.getElementById('host').attachShadow({mode: 'open'});
  root.innerHTML = '<button id="inner">Shadow button</button>';
</script>
</body></html>`

func TestPageJSInChrome(t *testing.T) {
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	t.Setenv("TB_WEB_HEADLESS", "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(testPage))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	d, err := Open(ctx, device.Info{Platform: device.Web, Serial: "chrome", Name: "Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.LaunchApp(ctx, srv.URL+"/", driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}

	var s *ui.Screen
	for deadline := time.Now().Add(10 * time.Second); ; {
		if s, err = d.Screen(ctx); err != nil {
			t.Fatal(err)
		}
		if find(s, "inner") != nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if n := find(s, "buy"); n == nil || n.Text != "Buy" || !n.Clickable {
		t.Errorf("button #buy: %+v", n)
	}
	if n := find(s, "email"); n == nil || n.Hint != "Email" || !n.Editable {
		t.Errorf("field #email: %+v", n)
	}
	if n := find(s, "inner"); n == nil || n.Text != "Shadow button" {
		t.Errorf("a button in a shadow root is listed: %+v", n)
	}
	// Not on screen: display:none, visibility:hidden, and a zero-height box that clips its
	// content (a banner about to grow).
	for _, id := range []string{"gone", "ghost", "banner"} {
		if n := find(s, id); n != nil {
			t.Errorf("#%s is not shown but was listed: %+v", id, n)
		}
	}
	for _, n := range s.Nodes {
		if strings.Contains(n.Text, "Banner text") || strings.Contains(n.Text, "Invisible text") {
			t.Errorf("text that is not shown was listed: %+v", n)
		}
	}
}

func find(s *ui.Screen, id string) *ui.Node {
	for _, n := range s.Nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

// What page.js reports for each kind of element: what counts as tappable, fields' text and
// state, id fallbacks, scrolling areas and same-origin frames.
const rulesPage = `<!doctype html><html><body style="margin:0">
<a id="link" href="#x">Link</a>
<a id="nolink">Anchor without href</a>
<div id="rolebtn" role="button">Role button</div>
<div id="handler" onclick="1">Has a click handler</div>
<div id="focusable" tabindex="0">Tab stop</div>
<div id="pointer" style="cursor:pointer">Pointer <span id="inner" style="cursor:pointer">inside</span></div>
<div id="plain">Plain text</div>
<input id="typed" value="Ada">
<textarea id="notes" placeholder="Notes"></textarea>
<input id="secret" type="password" value="hunter2">
<input id="agree" type="checkbox" checked>
<select id="size"><option>Small</option><option selected>Large</option></select>
<button id="off" disabled>Disabled</button>
<div id="ariaoff" role="button" aria-disabled="true">Aria disabled</div>
<button aria-label="Close dialog">x</button>
<button data-testid="save-btn">Save</button>
<div id="scroller" style="height:50px;overflow:auto"><div style="height:500px">Tall</div></div>
<iframe id="frame" srcdoc="<button id='framed'>In a frame</button>" style="height:60px"></iframe>
</body></html>`

func openPage(t *testing.T, html string) (*Driver, context.Context) {
	t.Helper()
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	t.Setenv("TB_WEB_HEADLESS", "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	d, err := Open(ctx, device.Info{Platform: device.Web, Serial: "chrome", Name: "Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.LaunchApp(ctx, srv.URL+"/", driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}
	return d, ctx
}

// screenWith reads the screen until the element with id shows (a page's late parts, a frame).
func screenWith(t *testing.T, d *Driver, ctx context.Context, id string) *ui.Screen {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		s, err := d.Screen(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if find(s, id) != nil || time.Now().After(deadline) {
			return s
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestPageJSRules(t *testing.T) {
	d, ctx := openPage(t, rulesPage)
	s := screenWith(t, d, ctx, "framed")
	get := func(id string) *ui.Node {
		n := find(s, id)
		if n == nil {
			t.Fatalf("#%s not listed", id)
		}
		return n
	}
	for _, id := range []string{"link", "rolebtn", "handler", "focusable", "pointer"} {
		if !get(id).Clickable {
			t.Errorf("#%s should be tappable", id)
		}
	}
	for _, id := range []string{"nolink", "plain", "inner"} {
		if n := find(s, id); n != nil && n.Clickable {
			t.Errorf("#%s should not be tappable (a pointer cursor counts where it starts)", id)
		}
	}
	if n := get("typed"); n.Text != "Ada" || n.Value != "Ada" || !n.Editable || n.ShowingHint {
		t.Errorf("field with a value: %+v", n)
	}
	if n := get("notes"); n.Text != "Notes" || !n.ShowingHint || n.Hint != "Notes" || !n.Editable {
		t.Errorf("empty field showing its placeholder: %+v", n)
	}
	if n := get("secret"); !n.Password {
		t.Errorf("password field: %+v", n)
	}
	if n := get("agree"); !n.Checkable || !n.Checked {
		t.Errorf("checked checkbox: %+v", n)
	}
	if n := get("size"); n.Text != "Large" {
		t.Errorf("select shows its chosen option: %+v", n)
	}
	if get("off").Enabled || get("ariaoff").Enabled {
		t.Error("disabled and aria-disabled elements should not be enabled")
	}
	if n := find(s, "Close dialog"); n == nil || n.Desc != "Close dialog" {
		t.Errorf("an aria-label is the description, and the id when there is none: %+v", n)
	}
	if n := find(s, "save-btn"); n == nil || n.Text != "Save" {
		t.Errorf("data-testid is the id when there is no other: %+v", n)
	}
	if !get("scroller").Scrollable {
		t.Error("#scroller scrolls")
	}
	// A same-origin frame's content is listed in its place (the frame itself is not), at its
	// place on the page: below the scrolling area that comes before it.
	if framed, scroller := get("framed"), get("scroller"); framed.Text != "In a frame" || framed.Bounds.T < scroller.Bounds.B {
		t.Errorf("framed button %+v, after %+v", framed, scroller)
	}
	if find(s, "frame") != nil {
		t.Error("the frame element is listed as well as its content")
	}
}

// Driving a page: a tap runs its click handler (and is reported as a change), typing goes to
// the focused field, and the hit test names a veil over an element.
const actionsPage = `<!doctype html><html><body style="margin:0">
<button id="count" style="width:200px;height:60px" onclick="this.textContent='Clicked ' + (++window.n)">Click me</button>
<input id="name" style="width:200px;height:40px">
<button id="under" style="width:200px;height:60px">Under</button>
<div id="veil" style="position:fixed;left:0;top:0;width:100%;height:100%;background:rgba(0,0,0,.3)"></div>
<script>window.n = 0; document.getElementById('veil').style.display = 'none';</script>
</body></html>`

func TestPageJSActions(t *testing.T) {
	d, ctx := openPage(t, actionsPage)
	s := screenWith(t, d, ctx, "count")
	btn := find(s, "count")
	if btn == nil {
		t.Fatal("no #count")
	}
	res, err := d.Tap(ctx, btn.Bounds.CX(), btn.Bounds.CY(), driver.ActionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Settle == driver.NoChange {
		t.Errorf("a tap that changed the page reported no change: %+v", res)
	}
	if s = screenWith(t, d, ctx, "count"); find(s, "count").Text != "Clicked 1" {
		t.Errorf("after the tap: %+v", find(s, "count"))
	}

	name := find(s, "name")
	if _, err := d.Tap(ctx, name.Bounds.CX(), name.Bounds.CY(), driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.TypeText(ctx, "Lin", driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if s = screenWith(t, d, ctx, "name"); find(s, "name").Value != "Lin" {
		t.Errorf("typed into the focused field: %+v", find(s, "name"))
	}

	under := find(s, "under")
	if what, err := d.CoveredAt(ctx, under.Index, under.Bounds.CX(), under.Bounds.CY()); err != nil || what != "" {
		t.Errorf("uncovered: %q, %v", what, err)
	}
	if err := d.eval(ctx, "document.getElementById('veil').style.display = 'block'", nil); err != nil {
		t.Fatal(err)
	}
	s = screenWith(t, d, ctx, "veil")
	under = find(s, "under")
	if what, err := d.CoveredAt(ctx, under.Index, under.Bounds.CX(), under.Bounds.CY()); err != nil || !strings.Contains(what, "veil") {
		t.Errorf("under the veil: %q, %v", what, err)
	}
}
