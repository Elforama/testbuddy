package web

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// shortShotTimeout makes a stalled screenshot give up quickly for the test.
func shortShotTimeout(t *testing.T, d time.Duration) {
	old := screenshotTimeout
	screenshotTimeout = d
	t.Cleanup(func() { screenshotTimeout = old })
}

// shotResult runs f with a watchdog: a call that never returns fails the test instead of
// hanging it.
func shotResult(t *testing.T, f func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the screenshot never returned: the step would hang")
		return nil
	}
}

// Issue #163: a Page.captureScreenshot the browser never answers (a window Chrome cannot
// draw, a stalled compositor or GPU) failed nothing: the step waited on it forever. It now
// gives up after screenshotTimeout, with an error that names the call, even when the caller's
// context has no deadline.
func TestScreenshotThatNeverAnswersTimesOut(t *testing.T) {
	shortShotTimeout(t, 300*time.Millisecond)
	page, d := inputPage(t)
	page.on("Page.captureScreenshot", func(cdpMsg) any { return noAnswer })
	start := time.Now()
	err := shotResult(t, func() error {
		_, err := d.Screenshot(context.Background(), 1, "png")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "Page.captureScreenshot") || !strings.Contains(err.Error(), "did not answer within") {
		t.Fatalf("Screenshot = %v, want a timeout that names Page.captureScreenshot", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("gave up after %s, want about %s", took, screenshotTimeout)
	}
	// A caller that gives up first gets its own reason, not the screenshot's bound.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Screenshot(ctx, 1, "png"); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled Screenshot = %v, want context.Canceled", err)
	}
}

// Issue #163: a recording starts with a screenshot of the page; one the browser never answers
// fails the start (the run goes on unrecorded) instead of hanging it, and leaves no recording
// half started.
func TestRecordingFirstFrameThatNeverAnswersTimesOut(t *testing.T) {
	useFakeEncoder(t)
	shortShotTimeout(t, 300*time.Millisecond)
	page, d := recordingPage(t)
	page.on("Page.captureScreenshot", func(cdpMsg) any { return noAnswer })
	err := shotResult(t, func() error {
		return d.StartRecording(context.Background(), filepath.Join(t.TempDir(), "r.mp4"))
	})
	if err == nil || !strings.Contains(err.Error(), "Page.captureScreenshot") || !strings.Contains(err.Error(), "did not answer within") {
		t.Fatalf("StartRecording = %v, want a timeout that names Page.captureScreenshot", err)
	}
	d.recMu.Lock()
	rec := d.rec
	d.recMu.Unlock()
	if rec != nil {
		t.Error("a recording is left started")
	}
	if n := len(page.calls("Page.startScreencast")); n != 0 {
		t.Errorf("screencast started %d times after the first frame failed", n)
	}
}

// Issue #163 in Chrome: a page whose main thread never yields draws no frame, so Chrome never
// answers Page.captureScreenshot. The screenshot gives up after its bound instead of hanging.
func TestScreenshotOfABusyPageInChrome(t *testing.T) {
	d, ctx := openPage(t, `<html><body><button id="go" style="width:200px;height:80px"
onclick="setTimeout(function(){document.body.style.background='red';while(true){}},50)">Go</button></body></html>`)
	shortShotTimeout(t, 2*time.Second)
	b := find(screenWith(t, d, ctx, "go"), "go")
	if _, err := d.Tap(ctx, b.Bounds.CX(), b.Bounds.CY(), driver.ActionOpts{NoSettle: true}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond) // the loop starts
	err := shotResult(t, func() error {
		_, err := d.Screenshot(context.Background(), 1, "png")
		return err
	})
	// A Chrome that still draws the page answers; one that does not must say why.
	if err != nil && !strings.Contains(err.Error(), "Page.captureScreenshot: the browser did not answer within") {
		t.Fatalf("Screenshot of a busy page = %v", err)
	}
	t.Logf("Screenshot of a busy page: %v", err)
}
