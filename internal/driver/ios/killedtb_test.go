package ios

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

const killedTBEnv = "TB_TEST_IOS_KILLED_TB"

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
