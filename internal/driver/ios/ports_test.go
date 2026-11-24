package ios

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func overlaps(a, b, n int) bool { return a < b+n && b < a+n }

func fromTheTop(t *testing.T) {
	old := pickStart
	pickStart = func(starts int) int { return starts - 1 }
	t.Cleanup(func() { pickStart = old })
}

func TestFreePortsTwoCallersGetDistinctPorts(t *testing.T) {
	fromTheTop(t)
	a := freePorts(t, 3)
	b := freePorts(t, 3)
	if overlaps(a, b, 3) {
		t.Errorf("both callers got ports from %d and %d: they overlap", a, b)
	}
}

func TestFreePortsReservedAcrossProcesses(t *testing.T) {
	fromTheTop(t)
	if os.Getenv("TB_FREEPORTS_CHILD") == "1" {
		fmt.Printf("port=%d\n", freePorts(t, 3))
		return
	}
	mine := freePorts(t, 3)
	cmd := exec.Command(os.Args[0], "-test.run=^TestFreePortsReservedAcrossProcesses$", "-test.count=1", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), "TB_FREEPORTS_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	theirs := -1
	for sc := bufio.NewScanner(strings.NewReader(string(out))); sc.Scan(); {
		if v, ok := strings.CutPrefix(sc.Text(), "port="); ok {
			theirs, _ = strconv.Atoi(v)
		}
	}
	if theirs < 0 {
		t.Fatalf("child printed no port:\n%s", out)
	}
	if overlaps(mine, theirs, 3) {
		t.Errorf("this process holds ports from %d, the other process got ports from %d: they overlap", mine, theirs)
	}
}
