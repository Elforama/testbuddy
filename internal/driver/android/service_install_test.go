package android

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// AND-005 deployment: silently retaining a differently signed helper leaves
// the old no-op service active while the shell runs new code. Exercise the
// actual install shell with a fake package manager, rather than matching text.
func TestServiceInstallRecoversOnlyOwnHelperSigningMismatch(t *testing.T) {
	for _, mismatch := range []bool{true, false} {
		t.Run(map[bool]string{true: "signing mismatch", false: "other install failure"}[mismatch], func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "calls")
			scripts := map[string]string{
				"cmp":      "#!/bin/sh\nexit 1\n",
				"sleep":    "#!/bin/sh\nexit 0\n",
				"settings": "#!/bin/sh\necho null\n",
				"pm": `#!/bin/sh
echo "$*" >> "$TASK_PM_LOG"
case "$1" in
path) echo package:/fake/old.apk ;;
uninstall) test "$2" = dev.testbuddy.agent || exit 2; touch "$TASK_PM_MARK"; echo Success ;;
install)
 if test -f "$TASK_PM_MARK"; then echo Success
 elif test "$TASK_PM_MISMATCH" = yes; then echo 'Failure [INSTALL_FAILED_UPDATE_INCOMPATIBLE: signatures do not match]'; exit 1
 else echo 'Failure [INSTALL_FAILED_INSUFFICIENT_STORAGE]'; exit 1; fi ;;
esac
`,
			}
			for name, body := range scripts {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0755); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("/bin/sh", "-c", serviceSetup("/data/local/tmp/agent.apk"))
			value := "no"
			if mismatch {
				value = "yes"
			}
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "TASK_PM_LOG="+log, "TASK_PM_MARK="+filepath.Join(dir, "removed"), "TASK_PM_MISMATCH="+value)
			output, err := cmd.CombinedOutput()
			calls, _ := os.ReadFile(log)
			if mismatch {
				if err != nil || !strings.Contains(string(calls), "uninstall dev.testbuddy.agent\n") || strings.Count(string(calls), "install ") != 3 {
					t.Fatalf("did not replace only the stale helper: error=%v output=%s calls=%s", err, output, calls)
				}
			} else if err == nil || strings.Contains(string(calls), "uninstall") || !strings.Contains(string(output), "INSTALL_FAILED_INSUFFICIENT_STORAGE") {
				t.Fatalf("silently ignored or destructively retried install failure: error=%v output=%s calls=%s", err, output, calls)
			}
		})
	}
}
