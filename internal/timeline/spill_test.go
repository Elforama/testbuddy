package timeline

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// image is n bytes that do not compress, different for each seed.
func image(seed, n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(int64(seed))).Read(b)
	return b
}

// referencePage is the page as tb wrote it before issue #65: the whole template executed
// once, with every screenshot in one in-memory CSS string.
func referencePage(t *testing.T, r *Recorder, dir string, data map[int][]byte) []byte {
	t.Helper()
	full := template.Must(template.New("timeline").Funcs(template.FuncMap{
		"indent": func(d int) template.CSS { return template.CSS(fmt.Sprintf("%dpx", min(d, 8)*22)) },
	}).Parse(pageHTML))
	rec := r.snapshot()
	rec.dir = dir
	v := build(rec)
	var css strings.Builder
	for _, s := range rec.shots {
		fmt.Fprintf(&css, ".shot-%d{background-image:url(data:%s;base64,%s)}\n", s.ID, s.Mime, base64.StdEncoding.EncodeToString(data[s.ID]))
	}
	v.Shots = template.CSS(css.String())
	var b bytes.Buffer
	if err := full.Execute(&b, v); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// record makes a small run of n steps, each with a tap and a screenshot of size bytes, and
// returns each screenshot's bytes by ID.
func record(r *Recorder, n, size int) map[int][]byte {
	data := map[int][]byte{}
	r.Line("", 0, "header")
	for i := 1; i <= n; i++ {
		s := r.Begin("", 0, "tapOn", fmt.Sprintf(`Tap on "Item %d"`, i), "/f/a.yaml", i)
		r.Gesture("", "tap", 1080, 2400, [2]int{540, 100 * i})
		img := image(i, size)
		r.Shot("", "image/jpeg", img, 1080, 2400)
		data[i] = img
		r.End(s, "", "passed", "")
	}
	r.Finish(Meta{Flow: "Many <shots>", Status: "passed", Version: "dev"})
	return data
}

// Issue #65: a recorder kept every screenshot's bytes in memory until the run ended. Now
// they go to a temp file as they are taken; at most a small bound stays in memory.
func TestShotsAreNotKeptInMemory(t *testing.T) {
	r := New()
	defer r.Close()
	record(r, 50, 64<<10)
	if n := r.memBytes(); n > 64<<10 {
		t.Errorf("after 50 screenshots of 64 KB the recorder holds %d bytes of them in memory, want at most one screenshot", n)
	}
}

// Issue #65: writing the page built every screenshot's base64 in one string and the whole
// page as one []byte (about 3x the screenshots' size). Now each screenshot is streamed
// from the temp file to the page, so writing allocates about one copy buffer.
func TestWriteFileStreamsScreenshots(t *testing.T) {
	const n, size = 40, 256 << 10 // 10 MB of screenshots, 13.7 MB as base64
	r := New()
	defer r.Close()
	record(r, n, size)
	path := filepath.Join(t.TempDir(), "timeline.html")
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := r.WriteFile(path); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if got, limit := after.TotalAlloc-before.TotalAlloc, uint64(2*size+(1<<20)); got > limit {
		t.Errorf("writing a page of %d screenshots of %d KB allocated %.1f MB, want at most %.1f MB",
			n, size>>10, float64(got)/(1<<20), float64(limit)/(1<<20))
	}
	if st, err := os.Stat(path); err != nil || st.Size() < n*size*4/3 {
		t.Errorf("page: %v, %v", st, err)
	}
}

// Issue #65: the page is byte for byte what the in-memory renderer wrote, from WriteFile
// and from HTML, with videos linked relative to the page's folder.
func TestPageBytesUnchanged(t *testing.T) {
	r := New()
	defer r.Close()
	dir := t.TempDir()
	data := record(r, 12, 3000)
	r.Video("", filepath.Join(dir, "recording.mp4"), r.start, r.start.Add(5e9))
	want := referencePage(t, r, dir, data)
	path := filepath.Join(dir, "timeline.html")
	if err := r.WriteFile(path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WriteFile wrote %d bytes, differing from the in-memory page's %d at byte %d", len(got), len(want), firstDiff(got, want))
	}
	if html, want := r.HTML(), referencePage(t, r, "", data); !bytes.Equal(html, want) {
		t.Errorf("HTML gave %d bytes, differing from the in-memory page's %d at byte %d", len(html), len(want), firstDiff(html, want))
	}
	// A screenshot in no step, and a page with no screenshots at all.
	empty := New()
	defer empty.Close()
	empty.Finish(Meta{Flow: "none"})
	if got, want := empty.HTML(), referencePage(t, empty, "", nil); !bytes.Equal(got, want) {
		t.Error("an empty page differs")
	}
}

func firstDiff(a, b []byte) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

// failingSpill is a temp file whose writes fail after ok of them.
type failingSpill struct {
	*os.File
	mu sync.Mutex
	ok int
}

func (f *failingSpill) WriteAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ok == 0 {
		// half written, as on a full disk
		n, _ := f.File.WriteAt(p[:len(p)/2], off)
		return n, errors.New("no space left on device")
	}
	f.ok--
	return f.File.WriteAt(p, off)
}

// Issue #65: when the temp file cannot be made or written, the screenshots stay in memory:
// the timeline loses none and the run does not fail, and it says so once.
func TestSpillFallsBackToMemory(t *testing.T) {
	t.Run("cannot create", func(t *testing.T) {
		r := New()
		defer r.Close()
		var warnings []string
		r.OnWarning(func(msg string) { warnings = append(warnings, msg) })
		r.newSpill = func() (spillFile, error) { return nil, errors.New("read-only file system") }
		data := record(r, 5, 1000)
		if n := r.memBytes(); n != 5*1000 {
			t.Errorf("memory holds %d bytes, want all 5000", n)
		}
		if got, want := r.HTML(), referencePage(t, r, "", data); !bytes.Equal(got, want) {
			t.Error("the page differs")
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], "read-only file system") {
			t.Errorf("warnings = %q, want one naming the error", warnings)
		}
	})
	t.Run("write fails", func(t *testing.T) {
		r := New()
		defer r.Close()
		var warnings []string
		r.OnWarning(func(msg string) { warnings = append(warnings, msg) })
		var spill *failingSpill
		r.newSpill = func() (spillFile, error) {
			f, err := os.CreateTemp(t.TempDir(), "spill-*")
			spill = &failingSpill{File: f, ok: 2}
			return spill, err
		}
		data := record(r, 6, 1000)
		if n := r.memBytes(); n != 4*1000 {
			t.Errorf("memory holds %d bytes, want the 4 screenshots after the failed write", n)
		}
		page := filepath.Join(t.TempDir(), "timeline.html")
		if err := r.WriteFile(page); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(page)
		if want := referencePage(t, r, filepath.Dir(page), data); !bytes.Equal(got, want) {
			t.Errorf("the page differs at byte %d", firstDiff(got, want))
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], "no space left") {
			t.Errorf("warnings = %q, want one naming the error", warnings)
		}
	})
}

// Issue #65: Close removes the temp file, whether or not the page was written, and a
// recorder that took no screenshot never makes one.
func TestCloseRemovesTempFile(t *testing.T) {
	r := New()
	record(r, 3, 1000)
	path := r.spillPath()
	if path == "" {
		t.Fatal("no temp file for the screenshots")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	r.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("temp file still there after Close: %v", err)
	}
	r.Close() // twice is fine
	r.Shot("", "image/jpeg", []byte("late"), 1, 1)
	if r.spillPath() != "" {
		t.Error("a screenshot after Close made a new temp file")
	}

	written := New()
	record(written, 2, 1000)
	path = written.spillPath()
	if err := written.WriteFile(filepath.Join(t.TempDir(), "timeline.html")); err != nil {
		t.Fatal(err)
	}
	written.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("temp file still there after WriteFile and Close: %v", err)
	}

	none := New()
	none.Begin("", 0, "assertVisible", "Assert", "", 0)
	if none.spillPath() != "" {
		t.Error("a temp file without screenshots")
	}
	none.Close()
	var nilRec *Recorder
	nilRec.Close()
	nilRec.OnWarning(func(string) {})
}

// Issue #65: the devices of a multi-device flow share one recorder; their screenshots go
// to the same temp file from several goroutines while the page is rendered (run with -race).
func TestConcurrentRolesShareTheTempFile(t *testing.T) {
	r := New()
	defer r.Close()
	roles := []string{"alice", "bob", "carol", "dave"}
	var wg sync.WaitGroup
	for k, role := range roles {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 25 {
				s := r.Begin(role, 0, "tapOn", "Tap", "", 0)
				r.Shot(role, "image/png", []byte(fmt.Sprintf("%s-%d-%s", role, i, strings.Repeat("x", 500*k))), 10, 10)
				r.End(s, "", "passed", "")
			}
		}()
	}
	wg.Add(1)
	go func() { defer wg.Done(); r.HTML() }()
	wg.Wait()
	page := string(r.HTML())
	for _, role := range roles {
		for i := range 25 {
			k := 0
			for j, x := range roles {
				if x == role {
					k = j
				}
			}
			enc := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s-%d-%s", role, i, strings.Repeat("x", 500*k))))
			if n := strings.Count(page, "base64,"+enc+")"); n != 1 {
				t.Fatalf("%s's screenshot %d is in the page %d times", role, i, n)
			}
		}
	}
}
