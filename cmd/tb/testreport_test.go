package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/report"
)

// issue #59: a flow skipped because an earlier ordered flow failed keeps its status and the
// reason in the reports (the scheduler gives it as the result's error).
func TestFlowReportSkippedKeepsReason(t *testing.T) {
	r := result("profile", engine.Skipped, errors.New("skipped: an ordered flow failed"), 0)
	fr := flowReport(r, "/debug/profile", "", nil)
	if fr.Status != "SKIPPED" || fr.Error != "skipped: an ordered flow failed" {
		t.Errorf("skipped flow = status %q, error %q; want SKIPPED with its reason", fr.Status, fr.Error)
	}
	if fr.DebugDir != "" {
		t.Errorf("skipped flow names a debug dir: %q", fr.DebugDir)
	}
}

// issue #59: tb test --json counts skipped flows as skipped, not passed, and each one
// carries its status and reason. passed and failed keep their names.
func TestTestJSONCountsSkipped(t *testing.T) {
	flows := []report.Flow{
		{Name: "login", Status: "SUCCESS"},
		{Name: "warn", Status: "WARNING"},
		{Name: "checkout", Status: "ERROR", Error: "Element not found"},
		{Name: "profile", Status: "SKIPPED", Error: "skipped: an ordered flow failed"},
	}
	var out struct {
		Passed  *int `json:"passed"`
		Failed  *int `json:"failed"`
		Skipped *int `json:"skipped"`
		Flows   []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"flows"`
	}
	b := testJSON(flows, false)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, b)
	}
	if out.Passed == nil || out.Failed == nil || out.Skipped == nil {
		t.Fatalf("missing a count (passed, failed, skipped):\n%s", b)
	}
	if *out.Passed != 2 || *out.Failed != 1 || *out.Skipped != 1 {
		t.Errorf("passed=%d failed=%d skipped=%d, want 2, 1, 1\n%s", *out.Passed, *out.Failed, *out.Skipped, b)
	}
	if f := out.Flows[3]; f.Status != "SKIPPED" || f.Error != "skipped: an ordered flow failed" {
		t.Errorf("skipped flow = %+v", f)
	}
}

// A run with nothing skipped still says "skipped": 0, so scripts can read it every time.
func TestTestJSONSkippedZero(t *testing.T) {
	var out map[string]any
	json.Unmarshal(testJSON([]report.Flow{{Name: "a", Status: "SUCCESS"}}, false), &out)
	if v, ok := out["skipped"]; !ok || v != float64(0) {
		t.Errorf("skipped = %v (present %v), want 0", v, ok)
	}
}
