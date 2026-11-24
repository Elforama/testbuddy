package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
)

const labelsPage = `<!doctype html><html><head><style>
body { margin: 0 }
.row { display: block; margin: 10px; padding: 10px; width: 200px; background: #eee }
.sr-only { position: absolute; left: 0; top: 0; width: 1px; height: 1px; padding: 0; overflow: hidden; clip: rect(0,0,0,0); white-space: nowrap; border: 0 }
.ghost { opacity: 0; position: absolute; left: 600px; top: 0; width: 24px; height: 24px; margin: 0 }
</style></head><body>
<label class="row"><input type="checkbox" id="dark" class="sr-only"><span>Dark mode</span></label>
<label class="row"><input type="checkbox" id="wifi" class="ghost"><span>Wi-Fi</span></label>
<div class="row"><input type="checkbox" id="eggs"><label for="eggs">Eggs</label></div>
<div class="row"><input type="checkbox" id="milk" class="sr-only" style="top:300px"><label for="milk">Milk</label></div>
<label class="row"><input type="checkbox" id="terms"><span aria-hidden="true">*</span>Accept terms<span hidden>secret</span><span class="sr-only">, required</span></label>
<label class="row"><span style="opacity:0;position:absolute;left:600px;top:200px"><input type="checkbox" id="faded"></span><span>Faded</span></label>
<div id="said">ready</div>
<script>
window.down = '';
document.addEventListener('pointerdown', (e) => { window.down = e.target.id || e.target.tagName.toLowerCase(); }, true);
</script>
</body></html>`

func TestPageJSLabelTextOnlyOnVisibleControls(t *testing.T) {
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	t.Setenv("TB_WEB_HEADLESS", "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(labelsPage))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	d, err := Open(ctx, device.Info{Platform: device.Web, Serial: "chrome", Name: "Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	dir := t.TempDir()

	tap := func(text, id string) (string, bool) {
		t.Helper()
		f, err := flow.ParseBytes(filepath.Join(dir, id+".yaml"), []byte("url: "+srv.URL+"/\n---\n- launchApp\n- assertVisible: ready\n- tapOn: \""+text+"\"\n"))
		if err != nil {
			t.Fatal(err)
		}
		var trace strings.Builder
		res := engine.New(d, engine.Options{LookupTimeout: 3 * time.Second, Trace: engine.NewTrace(&trace)}).RunFlow(ctx, f)
		if res.Status != engine.Passed {
			t.Fatalf("tapOn %q: %v\n%s", text, res.Err, trace.String())
		}
		var got struct {
			Down    string
			Checked bool
		}
		if err := d.eval(ctx, "({down: window.down, checked: document.getElementById('"+id+"').checked})", &got); err != nil {
			t.Fatal(err)
		}
		return got.Down, got.Checked
	}
	for _, c := range []struct{ text, id, want string }{
		{"Dark mode", "dark", "span"},
		{"Wi-Fi", "wifi", "span"},
		{"Milk", "milk", "label"},
		{"Faded", "faded", "span"},
	} {
		if down, checked := tap(c.text, c.id); down != c.want || !checked {
			t.Errorf("tapOn %q: the pointer went down on %q (want %s), #%s checked %v", c.text, down, c.want, c.id, checked)
		}
	}
	if down, checked := tap("Eggs", "eggs"); !checked || (down != "eggs" && down != "label") {
		t.Errorf("tapOn Eggs: down on %q, checked %v", down, checked)
	}

	s, err := d.Screen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"dark": "", "wifi": "", "milk": "", "faded": "", "eggs": "Eggs", "terms": "Accept terms"} {
		if n := find(s, id); n == nil || n.Text != want {
			t.Errorf("#%s: %+v, want text %q (hidden inputs keep none; hidden text is left out)", id, n, want)
		}
	}
}
