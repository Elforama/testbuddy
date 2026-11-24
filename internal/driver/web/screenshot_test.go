package web

import (
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

func shortShotTimeout(t *testing.T, d time.Duration) {
	old := screenshotTimeout
	screenshotTimeout = d
	t.Cleanup(func() { screenshotTimeout = old })
}

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
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Screenshot timeout = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("gave up after %s, want about %s", took, screenshotTimeout)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Screenshot(ctx, 1, "png"); !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "did not answer within") {
		t.Errorf("cancelled Screenshot = %v, want the caller's context.Canceled", err)
	}
	dctx, dcancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer dcancel()
	err = shotResult(t, func() error {
		_, err := d.Screenshot(dctx, 1, "png")
		return err
	})
	if !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "did not answer within") {
		t.Errorf("Screenshot past the caller's deadline = %v, want the caller's own deadline", err)
	}
}

func TestScreenshotLateReplyAfterTimeout(t *testing.T) {
	shortShotTimeout(t, 300*time.Millisecond)
	page, d := inputPage(t)
	first := make(chan int64, 1)
	page.on("Page.captureScreenshot", func(m cdpMsg) any { first <- m.ID; return noAnswer })
	err := shotResult(t, func() error {
		_, err := d.Screenshot(context.Background(), 1, "png")
		return err
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Screenshot = %v, want a timeout", err)
	}
	id := <-first
	page.send(map[string]any{"id": id, "result": map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("stale"))}})
	page.on("Page.captureScreenshot", func(cdpMsg) any {
		return map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("fresh"))}
	})
	var img []byte
	err = shotResult(t, func() error {
		var err error
		img, err = d.Screenshot(context.Background(), 1, "png")
		return err
	})
	if err != nil || string(img) != "fresh" {
		t.Fatalf("second Screenshot = %q, %v, want \"fresh\"", img, err)
	}
}

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

func TestScreenshotOfABusyPageInChrome(t *testing.T) {
	d, ctx := openPage(t, `<html><body><button id="go" style="width:200px;height:80px"
onclick="setTimeout(function(){document.body.style.background='red';while(true){}},50)">Go</button></body></html>`)
	shortShotTimeout(t, 2*time.Second)
	b := find(screenWith(t, d, ctx, "go"), "go")
	if _, err := d.Tap(ctx, b.Bounds.CX(), b.Bounds.CY(), driver.ActionOpts{NoSettle: true}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	err := shotResult(t, func() error {
		_, err := d.Screenshot(context.Background(), 1, "png")
		return err
	})
	if err != nil && !strings.Contains(err.Error(), "Page.captureScreenshot: the browser did not answer within") {
		t.Fatalf("Screenshot of a busy page = %v", err)
	}
	t.Logf("Screenshot of a busy page: %v", err)
}
