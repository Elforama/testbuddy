package ios

// The tb that recording_test.go's TestKilledTBStopsTheRecorder kills (issue #52). Not limited to
// Unix like that test: TestMain refers to it on every platform.

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// killedTBEnv makes the test binary a tb that starts a recording and waits to be killed
// (killedTB); its value is the recording's path. TestMain runs it.
const killedTBEnv = "TB_TEST_IOS_KILLED_TB"

// killedTB starts a recording on the stub simctl, says so on stdout and waits forever: the test
// kills it with SIGKILL. The stubs' PATH and $TB_STUB_DIR come from the test.
func killedTB(path string) int {
	if p, err := exec.LookPath("xcrun"); err != nil || !strings.Contains(p, "tb-driver-stubs-") {
		os.Stderr.WriteString("xcrun is not the stub: refusing to run\n")
		return 3
	}
	d := &Driver{info: testInfo()}
	if err := d.StartRecording(context.Background(), path); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		return 1
	}
	os.Stdout.WriteString("recording\n")
	select {}
}
