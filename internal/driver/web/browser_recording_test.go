package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

// Recording a real page: the video exists, plays as H.264 (or MPEG-4) and lasts as long as
// the recording did, though the page repaints only now and then. Needs Chrome and ffmpeg:
// runs with TB_BROWSER_TESTS=1.
func TestRecordingInChrome(t *testing.T) {
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	if _, err := ffmpegPath(); err != nil {
		t.Skip("needs ffmpeg")
	}
	t.Setenv("TB_WEB_HEADLESS", "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><html><body style="font:40px sans-serif"><div id="t">start</div><script>
  let n = 0;
  const i = setInterval(() => { document.getElementById('t').textContent = 'step ' + (++n); if (n === 4) clearInterval(i); }, 500);
</script></body></html>`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	d, err := Open(ctx, device.Info{Platform: device.Web, Serial: "chrome", Name: "Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.LaunchApp(ctx, srv.URL+"/", driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "recording.mp4")
	start := time.Now()
	if err := d.StartRecording(ctx, out); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second) // the page repaints at 0.5, 1, 1.5 and 2 s, then stays still
	if err := d.StopRecording(ctx); err != nil {
		t.Fatal(err)
	}
	wall := time.Since(start).Seconds()
	st, err := os.Stat(out)
	if err != nil || st.Size() < 2000 {
		t.Fatalf("video: %v, %d bytes", err, st.Size())
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Logf("ffprobe not found: duration not checked (%d bytes, %.2f s recorded)", st.Size(), wall)
		return
	}
	b, err := exec.Command(probe, "-v", "error", "-show_entries", "format=duration:stream=codec_name",
		"-of", "default=nw=1", out).Output()
	if err != nil {
		t.Fatal(err)
	}
	var dur float64
	codec := ""
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "duration="); ok {
			dur, _ = strconv.ParseFloat(v, 64)
		}
		if v, ok := strings.CutPrefix(l, "codec_name="); ok {
			codec = v
		}
	}
	t.Logf("video %.2f s (%s, %d bytes) for %.2f s recorded", dur, codec, st.Size(), wall)
	if dur < wall-0.7 || dur > wall+0.7 {
		t.Errorf("video lasts %.2f s, the recording %.2f s", dur, wall)
	}
	if codec != "h264" && codec != "mpeg4" {
		t.Errorf("codec = %q", codec)
	}
}
