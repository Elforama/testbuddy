package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/legacy"
	"github.com/elforama/testbuddy/internal/look"
	"github.com/elforama/testbuddy/internal/orchestrate"
	"github.com/elforama/testbuddy/internal/report"
	"github.com/elforama/testbuddy/internal/workspace"
)

const testUsage = `Usage: tb test <flow files or folders> [options]

Runs flows (Maestro-compatible YAML; existing Maestro flows run unchanged). Options:
  -e, --env KEY=VALUE        set a variable (repeatable)
  -p, --platform android|ios|web  run on this platform only
  --device, --udid a,b       devices to use (serial, UDID or name)
  --device role=DEVICE       pin a multi-device flow's role (repeatable): --device alice="iPhone 17"
  --config FILE              workspace config (default: config.yaml in the folder)
  --include-tags a,b         run flows with any of these tags
  --exclude-tags a,b         skip flows with any of these tags
  --shard-split N            spread flows over N devices (a work queue)
  --shard-all N              run every flow on each of N devices
  --format junit|html|html-detailed|noop
  --output FILE              report path (default report.xml / report.html)
  --test-suite-name NAME     JUnit suite name
  --debug-output DIR         where failure screenshots and logs go
  --flatten-debug-output     write debug files straight into --debug-output
  --retries N                re-run a failed flow up to N times (on another device if free)
  --keep-animations          do not turn Android animations off during the run
  --headless                 web: run Chrome without a window
  --screen-size WxH          web: browser window size (default 1280x800)
  --json                     print a JSON summary (for agents and scripts)
  --quiet                    print only failures and the summary
`

func cmdTest(argv []string) int {
	a, err := parseArgs(argv,
		flagSpec{[]string{"env", "e"}, true}, flagSpec{[]string{"platform", "p"}, true},
		flagSpec{[]string{"device", "udid", "d"}, true}, flagSpec{[]string{"config"}, true},
		flagSpec{[]string{"include-tags"}, true}, flagSpec{[]string{"exclude-tags"}, true},
		flagSpec{[]string{"shard-split", "shards", "s"}, true}, flagSpec{[]string{"shard-all"}, true},
		flagSpec{[]string{"format"}, true}, flagSpec{[]string{"output"}, true},
		flagSpec{[]string{"test-suite-name"}, true}, flagSpec{[]string{"debug-output"}, true},
		flagSpec{[]string{"test-output-dir"}, true}, flagSpec{[]string{"flatten-debug-output"}, false},
		flagSpec{[]string{"retries"}, true}, flagSpec{[]string{"keep-animations"}, false},
		flagSpec{[]string{"json"}, false}, flagSpec{[]string{"quiet", "q"}, false},
		flagSpec{[]string{"continuous", "c"}, false}, flagSpec{[]string{"headless"}, false},
		flagSpec{[]string{"screen-size"}, true},
		flagSpec{[]string{"reinstall-driver", "no-reinstall-driver"}, false},
		flagSpec{[]string{"help", "h"}, false},
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprint(os.Stderr, testUsage)
		return 2
	}
	if a.flag("help") || len(a.pos) == 0 {
		fmt.Print(testUsage)
		if len(a.pos) == 0 && !a.flag("help") {
			return 2
		}
		return 0
	}

	plan, err := workspace.Build(a.pos, workspace.Options{ConfigPath: a.str("config"),
		IncludeTags: a.list("include-tags"), ExcludeTags: a.list("exclude-tags")})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if a.flag("headless") {
		os.Setenv("TB_WEB_HEADLESS", "1")
	}
	if v := a.str("screen-size"); v != "" {
		os.Setenv("TB_WEB_SIZE", v)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Devices
	platform := strings.ToLower(a.str("platform"))
	all := device.List(ctx)
	var devs []device.Info
	for _, d := range all {
		if platform == "" || string(d.Platform) == platform {
			devs = append(devs, d)
		}
	}
	// --device takes devices (a,b) and role pins for multi-device flows (alice="iPhone 17").
	rolePins := map[string]string{} // role -> serial
	var want []string
	for _, w := range a.list("device") {
		if role, dev, ok := strings.Cut(w, "="); ok && role != "" {
			d, err := device.Pick(devs, "", strings.Trim(dev, `"'`))
			if err != nil {
				fmt.Fprintf(os.Stderr, "--device %s: %v\n", w, err)
				return 2
			}
			rolePins[role] = d.Serial
			continue
		}
		want = append(want, w)
	}
	if len(want) > 0 {
		var picked []device.Info
		have := map[string]bool{}
		for _, w := range want {
			d, err := device.Pick(devs, "", w)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
			picked = append(picked, d)
			have[d.Serial] = true
		}
		for _, serial := range rolePins { // pinned devices join the pool
			for _, d := range devs {
				if d.Serial == serial && !have[serial] {
					picked = append(picked, d)
					have[serial] = true
				}
			}
		}
		devs = picked
	}
	for role := range rolePins {
		known := false
		for _, f := range plan.Flows {
			for _, r := range f.Config.Devices {
				known = known || r.Name == role
			}
		}
		if !known {
			fmt.Fprintf(os.Stderr, "--device %s=...: no flow has a device role named %q\n", role, role)
			return 2
		}
	}
	if len(devs) == 0 {
		_, err := device.Pick(nil, device.Platform(platform), "")
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	multi := false
	for _, f := range plan.Flows {
		if len(f.Config.Devices) > 0 {
			multi = true
		}
	}
	// Every connected device is in the pool; each flow goes to a device that has its app.
	// Flows run one at a time unless sharding asks for more.
	shardSplit, shardAll := a.num("shard-split", 0), a.num("shard-all", 0)
	parallel := 1
	switch {
	case shardAll > 0:
		if shardAll > len(devs) {
			fmt.Fprintf(os.Stderr, "--shard-all %d needs %d devices; %d connected\n", shardAll, shardAll, len(devs))
			return 2
		}
		devs = devs[:shardAll]
		parallel = shardAll
	case shardSplit > 0:
		if shardSplit > len(devs) {
			fmt.Fprintf(os.Stderr, "--shard-split %d needs %d devices; %d connected\n", shardSplit, shardSplit, len(devs))
			return 2
		}
		parallel = shardSplit
	}
	_ = multi
	if a.flag("continuous") {
		fmt.Fprintln(os.Stderr, "note: --continuous is not supported yet; running once")
	}

	// Env: -e, then MAESTRO_ shell variables (as Maestro passes them), then the defaults (which win).
	var baseEnv []flow.KV
	seenEnv := map[string]bool{}
	for _, kv := range a.vals["env"] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			fmt.Fprintf(os.Stderr, "-e needs KEY=VALUE, got %q\n", kv)
			return 2
		}
		baseEnv = append(baseEnv, flow.KV{Key: k, Value: v})
		seenEnv[k] = true
	}
	// MAESTRO_ variables in the environment reach flows, as they do under Maestro.
	lp := legacy.EnvPrefix()
	for _, e := range os.Environ() {
		k, v, _ := strings.Cut(e, "=")
		if strings.HasPrefix(k, lp) && v != "" && !seenEnv[k] && k != lp+"SHARD_ID" && k != lp+"SHARD_INDEX" {
			baseEnv = append(baseEnv, flow.KV{Key: k, Value: v})
		}
	}

	// Debug output
	debugRoot := a.str("debug-output")
	if d := a.str("test-output-dir"); d != "" {
		debugRoot = d
	} else if plan.Config != nil && plan.Config.TestOutputDir != "" && debugRoot == "" {
		debugRoot = plan.Config.TestOutputDir
	}
	stamp := time.Now().Format("2006-01-02_150405")
	switch {
	case debugRoot == "":
		debugRoot = filepath.Join(daemon.Dir(), "tests", stamp)
	case !a.flag("flatten-debug-output"):
		debugRoot = filepath.Join(debugRoot, stamp)
	}

	jsonOut := a.flag("json")
	con := newConsole(os.Stdout, jsonOut || a.flag("quiet"), a.num("shard-split", 0) > 1 || a.num("shard-all", 0) > 1)

	// Jobs
	var jobs []*orchestrate.Job
	for i, f := range plan.Flows {
		if shardAll > 0 && len(f.Config.Devices) == 0 {
			for k, d := range devs {
				jobs = append(jobs, &orchestrate.Job{Flow: f, ShardID: k + 1,
					Needs: []orchestrate.Need{{Platform: platform, Device: d.Serial}}})
			}
			continue
		}
		needs := orchestrate.NeedsFor(f, platform)
		for k := range needs {
			if serial, ok := rolePins[needs[k].Role]; ok && needs[k].Role != "" {
				needs[k].Device = serial
			}
		}
		jobs = append(jobs, &orchestrate.Job{Flow: f, Needs: needs, Seq: i < plan.Sequential})
	}

	pool := orchestrate.NewPool(devs)
	defer pool.Close()
	pool.CheckApps(ctx, jobs)
	var restoreMu sync.Mutex
	var restores []func()
	shardOf := map[string]int{}
	for i, d := range devs {
		shardOf[d.Serial] = i + 1
	}

	var suiteMu sync.Mutex
	var flows []report.Flow
	results := orchestrate.Schedule(ctx, pool, jobs, a.num("retries", 0), parallel, plan.ContinueOnFail, orchestrate.Hooks{
		Prepare: func(ctx context.Context, d device.Info, drv driver.Driver) {
			type animator interface {
				DisableAnimations(context.Context) (func(), error)
			}
			if an, ok := drv.(animator); ok && !a.flag("keep-animations") {
				if restore, err := an.DisableAnimations(ctx); err == nil {
					restoreMu.Lock()
					restores = append(restores, restore)
					restoreMu.Unlock()
				}
			}
		},
		Options: func(j *orchestrate.Job, ds map[string]device.Info) engine.Options {
			env := append([]flow.KV(nil), baseEnv...)
			vars := []flow.KV{{Key: "FILENAME", Value: strings.TrimSuffix(filepath.Base(j.Flow.Path), filepath.Ext(j.Flow.Path))}}
			if d, ok := ds[""]; ok {
				shard := shardOf[d.Serial]
				vars = append(vars, flow.KV{Key: "DEVICE_UDID", Value: d.Serial},
					flow.KV{Key: "SHARD_ID", Value: fmt.Sprint(shard)},
					flow.KV{Key: "SHARD_INDEX", Value: fmt.Sprint(shard - 1)})
			}
			// TB_FILENAME, TB_DEVICE_UDID, TB_SHARD_ID, TB_SHARD_INDEX, and the same under
			// Maestro's names (MAESTRO_FILENAME, ...) for Maestro flows.
			for _, prefix := range []string{"TB_", legacy.EnvPrefix()} {
				for _, v := range vars {
					env = append(env, flow.KV{Key: prefix + v.Key, Value: v.Value})
				}
			}
			label := orchestrate.DeviceLabel(ds)
			return engine.Options{Env: env, ArtifactsDir: flowDebugDir(debugRoot, j), Reporter: con.reporter(j.Flow.Name(), label)}
		},
		Started: func(j *orchestrate.Job, ds map[string]device.Info) {
			con.flowStart(j, orchestrate.DeviceLabel(ds))
		},
		Finished: func(r orchestrate.Result) {
			dir := flowDebugDir(debugRoot, r.Job)
			if r.Status == engine.Failed {
				saveFailure(ctx, dir, r, pool)
			}
			con.flowEnd(r, dir)
			if r.Status == engine.Failed && r.Job.Attempt < a.num("retries", 0) {
				return // will be retried; reported again
			}
			fr := report.Flow{Name: r.Job.Flow.Name(), File: r.Job.Flow.Path, Device: orchestrate.DeviceLabel(r.Devices),
				Duration: r.Duration, Start: r.Start, Tags: r.Job.Flow.Config.Tags, Properties: r.Job.Flow.Config.Properties}
			switch r.Status {
			case engine.Failed:
				fr.Status = "ERROR"
				fr.DebugDir = dir
				if r.Err != nil {
					fr.Error = r.Err.Error()
				}
			case engine.Skipped:
				fr.Status = "SKIPPED"
			default:
				fr.Status = "SUCCESS"
			}
			for _, s := range r.Steps {
				fr.Steps = append(fr.Steps, report.Step{Desc: s.Desc, Status: string(s.Status), Error: s.Err, Duration: s.Duration})
			}
			suiteMu.Lock()
			flows = append(flows, fr)
			suiteMu.Unlock()
		},
	})
	for _, restore := range restores {
		restore()
	}

	failed := 0
	for _, r := range results {
		if r.Status == engine.Failed {
			failed++
		}
	}

	// Reports
	suiteName := a.str("test-suite-name")
	if suiteName == "" {
		suiteName = "Test Suite"
	}
	var names []string
	for _, d := range devs {
		names = append(names, d.Name)
	}
	suite := report.Suite{Name: suiteName, Device: strings.Join(names, ", "), Flows: flows, Start: time.Now()}
	switch strings.ToLower(strings.ReplaceAll(a.str("format"), "_", "-")) {
	case "junit":
		out := orDefault(a.str("output"), "report.xml")
		data, err := report.JUnit([]report.Suite{suite})
		if err == nil {
			err = os.WriteFile(out, data, 0o644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "report:", err)
		}
	case "html", "html-detailed":
		out := orDefault(a.str("output"), "report.html")
		if err := os.WriteFile(out, report.HTML([]report.Suite{suite}, strings.HasSuffix(strings.ToLower(a.str("format")), "detailed")), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "report:", err)
		}
	}

	if jsonOut {
		type jflow struct {
			Name     string  `json:"name"`
			File     string  `json:"file"`
			Device   string  `json:"device"`
			Status   string  `json:"status"`
			Seconds  float64 `json:"seconds"`
			Error    string  `json:"error,omitempty"`
			DebugDir string  `json:"debugDir,omitempty"`
		}
		var out struct {
			Passed int     `json:"passed"`
			Failed int     `json:"failed"`
			Flows  []jflow `json:"flows"`
		}
		for _, f := range flows {
			if f.Status == "ERROR" {
				out.Failed++
			} else {
				out.Passed++
			}
			out.Flows = append(out.Flows, jflow{f.Name, f.File, f.Device, f.Status, f.Duration.Seconds(), f.Error, f.DebugDir})
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
	} else {
		con.summary(results)
	}
	if ctx.Err() != nil {
		return 130
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func flowDebugDir(root string, j *orchestrate.Job) string {
	name := unsafeName.ReplaceAllString(j.Flow.Name(), "_")
	if j.ShardID > 0 {
		name += fmt.Sprintf("-shard-%d", j.ShardID)
	}
	if j.Attempt > 0 {
		name += fmt.Sprintf("-retry-%d", j.Attempt)
	}
	return filepath.Join(root, name)
}

// saveFailure writes what an agent or person needs to see why a flow failed: a screenshot,
// the screen listing and the error.
func saveFailure(ctx context.Context, dir string, r orchestrate.Result, pool *orchestrate.Pool) {
	os.MkdirAll(dir, 0o755)
	if r.Err != nil {
		os.WriteFile(filepath.Join(dir, "error.txt"), []byte(r.Err.Error()+"\n"), 0o644)
	}
	for role, d := range r.Devices {
		drv, err := pool.Driver(ctx, d)
		if err != nil {
			continue
		}
		suffix := ""
		if role != "" {
			suffix = "-" + role
		}
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if img, err := drv.Screenshot(c, 1, "png"); err == nil {
			os.WriteFile(filepath.Join(dir, "screenshot"+suffix+".png"), img, 0o644)
		}
		if s, err := drv.Screen(c); err == nil {
			os.WriteFile(filepath.Join(dir, "screen"+suffix+".txt"), []byte(look.Build(s, look.NewRefs()).Text()), 0o644)
			if b, err := json.MarshalIndent(hierarchyJSON(s), "", " "); err == nil {
				os.WriteFile(filepath.Join(dir, "hierarchy"+suffix+".json"), b, 0o644)
			}
		}
		cancel()
	}
	var steps []map[string]any
	for _, s := range r.Steps {
		steps = append(steps, map[string]any{"command": s.Desc, "status": s.Status, "error": s.Err, "durationMs": s.Duration.Milliseconds()})
	}
	if b, err := json.MarshalIndent(steps, "", " "); err == nil {
		os.WriteFile(filepath.Join(dir, "commands.json"), b, 0o644)
	}
}

var errUsage = errors.New("usage")
