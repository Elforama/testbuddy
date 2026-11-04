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
