package web

// Issue #79 review: which pending requests keep a ready page's unfinished load counting, the
// DOM clock that polling does not move, and a same-document back on a stalled load.

import (
	"encoding/json"
	"testing"
	"time"
)

func request(id, typ, u string) json.RawMessage {
	return raw(map[string]any{"requestId": id, "loaderId": "L1", "frameId": "main", "type": typ,
		"documentURL": "https://shop.example.com/", "request": map[string]string{"url": u}})
}

// What a ready page's unfinished load may be given up on: images, media, beacons, and
// third-party scripts and frames (ads, analytics) never answering do not hold it; the site's
// own scripts and frames, and stylesheets and fonts from anywhere, do.
func TestHoldsLoad(t *testing.T) {
	page := "https://shop.example.com/"
	for _, c := range []struct {
		typ, url string
		holds    bool
	}{
		{"Image", "https://shop.example.com/hero.png", false},
		{"Media", "https://shop.example.com/intro.mp4", false},
		{"Ping", "https://shop.example.com/beacon", false},
		{"XHR", "https://shop.example.com/api", false},
		{"Fetch", "https://shop.example.com/api", false},
		{"Script", "https://cdn.shop.example.com/app.js", true},
		{"Script", "https://ads.example.net/tag.js", false},
		{"Document", "https://shop.example.com/frame.html", true},
		{"Document", "https://ads.example.net/frame.html", false},
		{"Stylesheet", "https://cdn.example.net/site.css", true},
		{"Font", "https://fonts.example.net/a.woff2", true},
	} {
		if got := holdsLoad(c.typ, c.url, page); got != c.holds {
			t.Errorf("%s %s: holds %v, want %v", c.typ, c.url, got, c.holds)
		}
	}
}

// An app's own bundle still on its way holds the load however quiet the DOM is; once it ends,
// the load may be given up on (an image is still pending).
func TestPendingScriptHoldsLoad(t *testing.T) {
	t.Setenv("TB_CI", "0")
	d := newBareDriver()
	defer d.Close()
	startLoad(d)
	d.onEvent("Network.requestWillBeSent", request("img", "Image", "https://shop.example.com/pixel.png"), "")
	d.onEvent("Network.requestWillBeSent", request("js", "Script", "https://shop.example.com/app.js"), "")
	d.onEvent("Network.requestWillBeSent", request("ad", "Script", "https://ads.example.net/tag.js"), "")
	d.onEvent("Network.requestWillBeSent", raw(map[string]any{"requestId": "L1", "loaderId": "L1", "frameId": "main",
		"type": "Document", "documentURL": "https://shop.example.com/", "request": map[string]string{"url": "https://shop.example.com/"}}), "")
	backdate(d)
	if stale, _ := staleNow(d); stale {
		t.Fatal("gave up on the load while the site's own script was pending")
	}
	locked(d, func() {
		if len(d.loadPending) != 1 {
			t.Errorf("held requests %v, want the site's script only", d.loadPending)
		}
	})
	d.onEvent("Network.loadingFinished", raw(map[string]string{"requestId": "js"}), "frame-session") // another session's id
	backdate(d)
	if stale, _ := staleNow(d); stale {
		t.Fatal("a frame session's request end released the page's script")
	}
	d.onEvent("Network.loadingFinished", raw(map[string]string{"requestId": "js"}), "")
	backdate(d)
	if stale, _ := staleNow(d); !stale {
		t.Error("the script ended and the DOM is quiet, but the load still counts")
	}

	// A new document drops the old one's requests; a held request counts up to the load
	// timeout, not for ever.
	d.onEvent("Page.frameStartedLoading", raw(map[string]string{"frameId": "main"}), "")
	d.onEvent("Page.frameNavigated", raw(map[string]any{"frame": map[string]string{"id": "main", "url": "https://shop.example.com/2"}}), "")
	locked(d, func() {
		if d.loadPending != nil {
			t.Error("a new document kept the old one's requests")
		}
	})
	d.onEvent("Page.domContentEventFired", raw(map[string]any{}), "")
	d.onEvent("Network.requestWillBeSent", request("js2", "Script", "https://shop.example.com/hang.js"), "")
	backdate(d)
	if stale, _ := staleNow(d); stale {
		t.Fatal("gave up on a just-started script")
	}
	locked(d, func() { d.loadPending["js2"] = time.Now().Add(-loadTimeout) })
	backdate(d)
	if stale, _ := staleNow(d); !stale {
		t.Error("a script pending past the load timeout still held the load")
	}
}

// Requests starting and ending (a page polling every second) are not the DOM changing, so
// they do not put back giving up on a stalled load.
func TestPollingDoesNotHoldStalledLoad(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	startLoad(d)
	locked(d, func() { d.domAt = time.Now().Add(-time.Hour) })
	d.onEvent("Network.requestWillBeSent", request("p1", "Fetch", "https://shop.example.com/poll"), "")
	d.onEvent("Network.loadingFinished", raw(map[string]string{"requestId": "p1"}), "")
	d.onStale()
	if stale, _ := staleNow(d); !stale {
		t.Error("a poll put back giving up on the stalled load")
	}
}

// Going back to a fragment entry on a page whose load never ended starts a load with no
// DOMContentLoaded of its own (Chrome: frameStartedLoading, then navigatedWithinDocument,
// and nothing else). The document is still the ready one.
func TestSameDocumentBackOnStalledLoad(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	startLoad(d)
	backdate(d)
	d.onEvent("Page.frameStartedLoading", raw(map[string]string{"frameId": "main"}), "")
	d.onEvent("Page.navigatedWithinDocument", raw(map[string]any{"frameId": "main", "url": "https://shop.example.com/#frag"}), "")
	if stale, ready := staleNow(d); stale || !ready {
		t.Fatalf("after a same-document back: stale %v ready %v, want waiting on a ready page", stale, ready)
	}
	backdate(d)
	if stale, _ := staleNow(d); !stale {
		t.Error("the load is not given up on again after a same-document back")
	}
	eventually(t, "the page settles after a same-document back", func() bool { return settledNow(d) })

	// A new document is not ready until its own DOMContentLoaded.
	d.onEvent("Page.frameStartedLoading", raw(map[string]string{"frameId": "main"}), "")
	d.onEvent("Page.frameNavigated", raw(map[string]any{"frame": map[string]string{"id": "main", "url": "https://shop.example.com/next"}}), "")
	d.onEvent("Page.navigatedWithinDocument", raw(map[string]any{"frameId": "main", "url": "https://shop.example.com/next#a"}), "")
	if _, ready := staleNow(d); ready {
		t.Error("a new document counted as ready before its DOMContentLoaded")
	}
}
