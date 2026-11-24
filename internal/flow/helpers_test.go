package flow

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const hdr = "appId: com.example\n---\n"

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func mustParse(t *testing.T, src string) *Flow {
	t.Helper()
	f, err := parse(t, src)
	if err != nil {
		t.Fatalf("parse: %v\n--- src ---\n%s", err, src)
	}
	return f
}

func steps(t *testing.T, body string) []*Command {
	t.Helper()
	return mustParse(t, hdr+body).Commands
}

func step(t *testing.T, body string) *Command {
	t.Helper()
	cs := steps(t, body)
	if len(cs) != 1 {
		t.Fatalf("want 1 command, got %d", len(cs))
	}
	return cs[0]
}

func asFlowErr(t *testing.T, err error) *Error {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatalf("want *flow.Error, got %T: %v", err, err)
	}
	return fe
}

func parseErr(t *testing.T, src string) *Error {
	t.Helper()
	_, err := parse(t, src)
	if err == nil {
		t.Fatalf("expected an error for:\n%s", src)
	}
	return asFlowErr(t, err)
}

type errCase struct {
	name string
	src  string
	want string
	line int
}

func runErrCases(t *testing.T, prefix string, cases []errCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := parseErr(t, prefix+tc.src)
			if !strings.Contains(e.Msg, tc.want) {
				t.Errorf("msg %q, want it to contain %q", e.Msg, tc.want)
			}
			if tc.line != 0 && e.Line != tc.line {
				t.Errorf("line %d, want %d (%v)", e.Line, tc.line, e)
			}
		})
	}
}

func names(cs []*Command) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}
