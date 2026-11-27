package session

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
)

type failedDo struct {
	Query string `json:"query"`
	Steps []struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	} `json:"steps"`
	Similar []struct {
		Ref   int    `json:"ref"`
		Label string `json:"label"`
		ID    string `json:"id"`
	} `json:"similar"`
}

func failDo(t *testing.T, commands string) (string, failedDo) {
	t.Helper()
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(button("Submit order", 100), item{id: "submit", class: "android.widget.ImageButton", top: 200}, label("Welcome", 300), button("Exit", 400))
	addDev(s, f, "")
	resp, _ := call(t, s.do, map[string]any{"commands": commands, "json": true}, t.TempDir())
	if resp.OK || resp.Exit != 1 {
		t.Fatalf("resp %+v", resp)
	}
	var result failedDo
	if err := json.Unmarshal(resp.JSON, &result); err != nil {
		t.Fatal(err)
	}
	return resp.Text, result
}

func TestDoFailureSuggestsDespiteTypoInText(t *testing.T) {
	text, result := failDo(t, `- tapOn: "Sumbit order"`)
	if !strings.Contains(text, `  similar: @1 Button "Submit order"`) {
		t.Errorf("no suggestion for a typo:\n%s", text)
	}
	if result.Query != "Sumbit order" || len(result.Similar) == 0 || result.Similar[0].Label != "Submit order" || result.Similar[0].Ref != 1 {
		t.Errorf("structured result: %+v", result)
	}
}

func TestDoFailureDoesNotSuggestAnotherShortWord(t *testing.T) {
	text, result := failDo(t, `- tapOn: Edit`)
	if strings.Contains(text, "similar:") || len(result.Similar) != 0 || result.Query != "Edit" {
		t.Errorf("Edit suggested something:\n%s\n%+v", text, result)
	}
}

func TestDoFailureQueryIsTheTextOrElseTheID(t *testing.T) {
	_, result := failDo(t, `- tapOn: {text: "Sumbit order", id: sumbit}`)
	if result.Query != "Sumbit order" {
		t.Errorf("query %q, want the text alone", result.Query)
	}
	_, result = failDo(t, `- tapOn: {id: sumbit}`)
	if result.Query != "sumbit" {
		t.Errorf("query %q, want the id", result.Query)
	}
}

func TestDoFailureOfANestedOrMixedCondition(t *testing.T) {
	text, result := failDo(t, "- runFlow:\n    commands:\n      - extendedWaitUntil: {visible: \"Sumbit order\", timeout: 100}")
	if !strings.Contains(text, `  similar: @1 Button "Submit order"`) || result.Query != "Sumbit order" {
		t.Errorf("no suggestion for a wait nested in runFlow:\n%s\n%+v", text, result)
	}
	text, result = failDo(t, `- extendedWaitUntil: {visible: "Submit order", notVisible: Welcome, timeout: 100}`)
	if strings.Contains(text, "similar:") || result.Query != "" || len(result.Similar) != 0 {
		t.Errorf("the visible half was found, yet it is the query:\n%s\n%+v", text, result)
	}
}

func TestDoFailureSuggestsForIDSelector(t *testing.T) {
	text, result := failDo(t, `- tapOn: {id: sumbit}`)
	if !strings.Contains(text, "  similar: ") || !strings.Contains(text, "#submit") {
		t.Errorf("no suggestion for an id:\n%s", text)
	}
	found := false
	for _, e := range result.Similar {
		found = found || e.ID == "submit"
	}
	if result.Query != "sumbit" || !found {
		t.Errorf("structured result: %+v", result)
	}
}

func TestDoFailureSuggestsForFailedVisibleAssertion(t *testing.T) {
	for _, commands := range []string{
		`- extendedWaitUntil: {visible: "Sumbit order", timeout: 100}`,
		`- extendedWaitUntil: {visible: {id: sumbit}, timeout: 100}`,
		`- assertVisible: "Sumbit order"`,
		`- assertVisible: {id: sumbit}`,
	} {
		t.Run(commands, func(t *testing.T) {
			text, result := failDo(t, commands)
			if !strings.Contains(text, "Assertion is false") {
				t.Fatalf("not a failed assertion:\n%s", text)
			}
			if !strings.Contains(text, "  similar: @") || len(result.Similar) == 0 || result.Query == "" {
				t.Errorf("no suggestion for a failed visible assertion:\n%s\n%+v", text, result)
			}
		})
	}
}

func TestDoFailureReason(t *testing.T) {
	cases := []struct{ commands, reason string }{
		{"- inputText: a\n- tapOn: \"Sumbit order\"", "not_found"},
		{"- inputText: a\n- tapOn: {id: sumbit}", "not_found"},
		{"- inputText: a\n- assertVisible: Nope", "assertion_failed"},
		{"- inputText: a\n- extendedWaitUntil: {visible: Nope, timeout: 100}", "assertion_failed"},
		{"- inputText: a\n- assertNotVisible: Welcome", "assertion_failed"},
		{"- inputText: a\n- assertTrue: ${1 == 2}", "assertion_failed"},
		{"- inputText: a\n- evalScript: ${nope.x.y}", "other"},
	}
	for _, c := range cases {
		t.Run(c.commands, func(t *testing.T) {
			_, result := failDo(t, c.commands)
			if len(result.Steps) != 2 || result.Steps[1].Status != "failed" {
				t.Fatalf("steps %+v", result.Steps)
			}
			if result.Steps[0].Reason != "" {
				t.Errorf("a passed step has a reason: %q", result.Steps[0].Reason)
			}
			if result.Steps[1].Reason != c.reason {
				t.Errorf("reason %q, want %q", result.Steps[1].Reason, c.reason)
			}
		})
	}
}

func TestDoWarnedStepHasNoReason(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(button("Log in", 100))
	addDev(s, f, "")
	resp, _ := call(t, s.do, map[string]any{"commands": "- tapOn: {text: Nope, optional: true}", "json": true}, "")
	if !resp.OK || strings.Contains(string(resp.JSON), `"reason"`) {
		t.Errorf("resp %+v\n%s", resp, resp.JSON)
	}
}
