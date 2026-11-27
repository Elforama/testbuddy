package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/legacy"
	"github.com/elforama/testbuddy/internal/look"
	marks "github.com/elforama/testbuddy/internal/mark"
	"github.com/elforama/testbuddy/internal/orchestrate"
	"github.com/elforama/testbuddy/internal/report"
	"github.com/elforama/testbuddy/internal/session"
	"github.com/elforama/testbuddy/internal/workspace"
)

const testUsage = `Usage: tb test <flow files or folders> [options]

Runs flows (Maestro-compatible YAML; existing Maestro flows run unchanged). Options:
  -e, --env KEY=VALUE        set a variable (repeatable)
  -p, --platform android|ios|web  the platform to run on: required unless a device is named
                             (--device, a connected TB_DEVICE or a session's name) or every
                             flow names it (url:, devices: roles)
  --device, --udid a,b       devices to use (serial, UDID or name)
  --device role=DEVICE       pin a multi-device flow's role (repeatable): --device alice="iPhone 17"
                             (TB_DEVICE: the device a run of one device at a time tries first; the
                             run keeps to its platform; not used when every flow names its own)
  --config FILE              workspace config (default: config.yaml in the folder)
  --include-tags a,b         run flows with any of these tags
  --exclude-tags a,b         skip flows with any of these tags
  --shard-split N            spread flows over N devices (a work queue)
  --shard-all N              run every flow on each of N devices
  --format junit|html|html-detailed|noop
  --output FILE              report path (default report.xml / report.html)
  --test-suite-name NAME     JUnit suite name
  --debug-output DIR         where each flow's log.txt and failure files go
  --timeline                 also write timeline.html in each flow's debug folder: every step
                             with the screen before and after it, where each tap and swipe
                             went, and its trace (screenshots make the run slower)
  --record                   record each flow's screen from start to finish: recording.mp4
                             in its debug folder, linked from the reports and the timeline
                             (web needs ffmpeg)
  --annotate[=TIME]          with --record: a video a person can follow (one for a PR): each
                             tap or swipe's result stays up TIME after it, and other steps
                             that act last at least TIME (default 1s; e.g. 0.5s, 500ms, 3s,
                             or seconds: 2; 0 for no holds), and the video shows a ring
                             where each touch went and, in a band added below the recording,
                             each step's text (placed with ffmpeg); not for ordinary runs
  --events-port N            with --annotate: take events for the videos on this port of
                             127.0.0.1 (default 7357; 0 for none). Each event sent while a
                             flow records shows in a band below the step's text, with its
                             time: "tb mark TEXT", or POST /event from the app (an Android
                             device reaches it on its own localhost; the URL is also in
                             $TB_EVENTS_URL and the flows' ${TB_EVENTS_URL})
  --flatten-debug-output     write debug files straight into --debug-output
  --retries N                re-run a failed flow up to N times (on another device if free)
  --from N                   one flow, on a phone: start at its Nth step, on the screen the
                             device is on now (onFlowStart and the steps before N do not
                             run: the reports list them as skipped; a retry starts at N too);
                             a failed run says the step to pass
  --keep-animations          do not turn Android animations off during the run
  --keep-agents              leave tb's agent running on each device after the run (the next
                             run starts faster; other test tools cannot use the device until
                             it idles out or "tb release")
  --wait-for-device TIME     a device another tb holds (another run, an open session): wait
                             up to TIME (e.g. 5m) for it instead of failing at once
  --headless                 web: run Chrome without a window
  --screen-size WxH          web: browser window size, 200 to 10000 a side (default 1280x800)
  --json                     print a JSON summary (for agents and scripts)
  --quiet                    print only failures and the summary
`

func cmdTest(argv []string) int {
	a, err := parseArgs(argv,
		flagSpec{[]string{"env", "e"}, textFlag}, flagSpec{[]string{"platform", "p"}, textFlag},
		flagSpec{[]string{"device", "udid", "d"}, textFlag}, flagSpec{[]string{"config"}, textFlag},
		flagSpec{[]string{"include-tags"}, textFlag}, flagSpec{[]string{"exclude-tags"}, textFlag},
		flagSpec{[]string{"shard-split", "shards", "s"}, intFlag}, flagSpec{[]string{"shard-all"}, intFlag},
		flagSpec{[]string{"format"}, textFlag}, flagSpec{[]string{"output"}, textFlag},
		flagSpec{[]string{"test-suite-name"}, textFlag}, flagSpec{[]string{"debug-output"}, textFlag},
		flagSpec{[]string{"test-output-dir"}, textFlag}, flagSpec{[]string{"flatten-debug-output"}, boolFlag},
		flagSpec{[]string{"retries"}, intFlag}, flagSpec{[]string{"from"}, intFlag}, flagSpec{[]string{"keep-animations"}, boolFlag}, flagSpec{[]string{"keep-agents"}, boolFlag},
		flagSpec{[]string{"timeline"}, boolFlag}, flagSpec{[]string{"record"}, boolFlag}, flagSpec{[]string{"annotate"}, optTimeFlag},
		flagSpec{[]string{"events-port"}, intFlag},
		flagSpec{[]string{"json"}, boolFlag}, flagSpec{[]string{"quiet", "q"}, boolFlag},
		flagSpec{[]string{"continuous", "c"}, boolFlag}, flagSpec{[]string{"headless"}, boolFlag},
		flagSpec{[]string{"screen-size"}, sizeFlag}, flagSpec{[]string{"wait-for-device"}, textFlag},
		flagSpec{[]string{"reinstall-driver", "no-reinstall-driver"}, boolFlag},
		flagSpec{[]string{"help", "h"}, boolFlag},
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
	if a.str("from") != "" {
		if err := startFrom(plan, a.num("from", 0)); err != nil {
			fmt.Fprintf(os.Stderr, "--from: %v\n", err)
			return 2
		}
	}
	return runTest(argv, a, plan, false)
}

func startFrom(plan *workspace.Plan, n int) error {
	if len(plan.Flows) != 1 {
		return fmt.Errorf("name one flow, not %d", len(plan.Flows))
	}
	f := plan.Flows[0]
	if len(f.Config.Devices) > 0 {
		return fmt.Errorf("not for a flow of several devices")
	}
	if f.Config.Web() {
		return fmt.Errorf("not for a web flow: each run opens a new browser")
	}
	if n < 1 || n > len(f.Commands) {
		return fmt.Errorf("%s has steps 1 to %d", filepath.Base(f.Path), len(f.Commands))
	}
	return nil
}

func runTest(argv []string, a *args, plan *workspace.Plan, replay bool) int {
	if a.flag("annotate") && !a.flag("record") {
		fmt.Fprintln(os.Stderr, "--annotate is for recordings: use it with --record")
		return 2
	}
	if a.str("events-port") != "" && !a.flag("annotate") {
		fmt.Fprintln(os.Stderr, "--events-port is for annotated recordings: use it with --record --annotate")
		return 2
	}
	var annotateStep time.Duration
	if v := a.str("annotate"); v != "" {
		d, err := parseAnnotateStep(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "--annotate: %v\n", err)
			return 2
		}
		annotateStep = d
	}
	if a.flag("headless") {
		os.Setenv("TB_WEB_HEADLESS", "1")
	}
	if v := a.str("screen-size"); v != "" {
		os.Setenv("TB_WEB_SIZE", v)
	}
	ctx, cancel := stopContext()
	defer cancel()

	platform := strings.ToLower(a.str("platform"))
	all := listDevices(ctx)
	var devs []device.Info
	for _, d := range all {
		if platform == "" || string(d.Platform) == platform {
			devs = append(devs, d)
		}
	}
	deviceArgs := a.list("device")
	single := len(deviceArgs) == 0 && a.num("shard-split", 0) == 0 && a.num("shard-all", 0) == 0 && !hasMultiDeviceFlow(plan)
	preferred, envMissed := "", false
	if env := strings.TrimSpace(os.Getenv("TB_DEVICE")); single && platform == "" && env != "" && !flowsNamePlatform(plan) {
		d, err := device.Pick(devs, "", env)
		if err != nil {
			if serial := sessionDevice(ctx, env); serial != "" {
				d, err = device.Pick(devs, "", serial)
			}
		}
		if err == nil {
			preferred = d.Serial
			platform = string(d.Platform)
			var same []device.Info
			for _, o := range devs {
				if o.Platform == d.Platform {
					same = append(same, o)
				}
			}
			devs = preferFirst(same, d.Serial)
		} else {
			envMissed = true
		}
	}
	rolePins := map[string]string{}
	var want []string
	for _, w := range deviceArgs {
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
	if platform == "" && len(want) == 0 && !flowsNamePlatform(plan) {
		if envMissed {
			fmt.Fprintf(os.Stderr, "TB_DEVICE=%s names no connected device or open session; ", strings.TrimSpace(os.Getenv("TB_DEVICE")))
		}
		fmt.Fprintln(os.Stderr, noPlatform)
		return 2
	}
	if len(want) > 0 {
		var picked []device.Info
		have := map[string]bool{}
		for _, w := range want {
			pick := device.Pick
			if replay {
				pick = pickReplayDevice
			}
			d, err := pick(devs, "", w)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
			picked = append(picked, d)
			have[d.Serial] = true
		}
		for _, serial := range rolePins {
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
	if phones(devs) == 0 && single && !ci.On() && needsPhone(plan) && startDevice(ctx, device.Platform(platform), func(s string) { fmt.Fprintln(os.Stderr, s) }) {
		devs = nil
		for _, d := range listDevices(ctx) {
			if platform == "" || string(d.Platform) == platform {
				devs = append(devs, d)
			}
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
	chosenForYou := single && parallel == 1 && phones(devs) > 1
	if a.flag("continuous") {
		fmt.Fprintln(os.Stderr, "note: --continuous is not supported yet; running once")
	}

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
	lp := legacy.EnvPrefix()
	for _, e := range os.Environ() {
		k, v, _ := strings.Cut(e, "=")
		if strings.HasPrefix(k, lp) && v != "" && !seenEnv[k] && k != lp+"SHARD_ID" && k != lp+"SHARD_INDEX" {
			baseEnv = append(baseEnv, flow.KV{Key: k, Value: v})
		}
	}

	debugRoot := a.str("debug-output")
	if d := a.str("test-output-dir"); d != "" {
		debugRoot = d
	} else if plan.Config != nil && plan.Config.TestOutputDir != "" && debugRoot == "" {
		debugRoot = plan.Config.TestOutputDir
	}
	stamp := time.Now().Format("2006-01-02_150405")
	switch {
	case debugRoot == "":
		debugRoot = claimDir(filepath.Join(daemon.Dir(), "tests"), stamp)
	case !a.flag("flatten-debug-output"):
		debugRoot = claimDir(debugRoot, stamp)
	}

	jsonOut := a.flag("json")
	con := newConsole(consoleWriter(jsonOut), jsonOut || a.flag("quiet"), a.num("shard-split", 0) > 1 || a.num("shard-all", 0) > 1)
	con.fromHint = !replay

	events, eventsURL := serveEvents(a, con)
	if events != nil {
		defer events.close()
		os.Setenv(marks.EnvURL, eventsURL)
		if !seenEnv[marks.EnvURL] {
			baseEnv = append(baseEnv, flow.KV{Key: marks.EnvURL, Value: eventsURL})
		}
	}

	var jobs []*orchestrate.Job
	for i, f := range plan.Flows {
		if shardAll > 0 && len(f.Config.Devices) == 0 {
			for k, d := range devs {
				jobs = append(jobs, &orchestrate.Job{Flow: f, ShardID: k + 1,
					Needs: []orchestrate.Need{{Platform: platform, Device: d.Serial}}})
			}
			continue
		}
		needs := orchestrate.NeedsFor(f, platform, baseEnv)
		for k := range needs {
			if serial, ok := rolePins[needs[k].Role]; ok && needs[k].Role != "" {
				needs[k].Device = serial
			}
		}
		jobs = append(jobs, &orchestrate.Job{Flow: f, Needs: needs, Seq: i < plan.Sequential})
	}
	nameDebugDirs(jobs)

	pool := orchestrate.NewPool(devs)
	defer pool.Close()
	pool.LockAs = "tb test " + strings.Join(argv, " ")
	if replay {
		pool.LockAs = "tb replay " + plan.Flows[0].Path
	}
	pool.KeepAgents = a.flag("keep-agents")
	pool.Reclaim = reclaimFromOwnSession
	pool.Prefer = preferred
	if w := a.str("wait-for-device"); w != "" {
		d, err := parseWait(w)
		if err != nil {
			fmt.Fprintf(os.Stderr, "--wait-for-device: %v\n", err)
			return 2
		}
		pool.WaitForDevice = d
	}
	pool.CheckApps(ctx, jobs)
	var restoreMu sync.Mutex
	restores := map[string]func(){}
	shardOf := map[string]int{}
	for i, d := range devs {
		shardOf[d.Serial] = i + 1
	}

	announced := ""
	var pagesMu sync.Mutex
	pages := map[*orchestrate.Job]string{}
	logs := newFlowLogs()
	timelines := newFlowTimelines(a.flag("timeline"))
	defer timelines.closeAll()
	results := orchestrate.Schedule(ctx, pool, jobs, a.num("retries", 0), parallel, plan.ContinueOnFail, orchestrate.Hooks{
		Prepare: func(ctx context.Context, d device.Info, drv driver.Driver) {
			events.reach(ctx, d)
			if a.flag("keep-animations") {
				return
			}
			if restore := disableAnimations(ctx, drv); restore != nil {
				restoreMu.Lock()
				restores[d.Serial] = restore
				restoreMu.Unlock()
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
			for _, prefix := range []string{"TB_", legacy.EnvPrefix()} {
				for _, v := range vars {
					env = append(env, flow.KV{Key: prefix + v.Key, Value: v.Value})
				}
			}
			label := orchestrate.DeviceLabel(ds)
			dir := flowDebugDir(debugRoot, j)
			trace := logs.open(dir, j, ds)
			return engine.Options{FromStep: a.num("from", 0), Env: env, ArtifactsDir: dir, Reporter: con.reporter(j.Flow.Name(), label),
				Trace: trace, Timeline: timelines.open(dir, trace), Record: a.flag("record"), Annotate: a.flag("annotate"), AnnotateStep: annotateStep,
				AnnotateEvents: events.forJob(ds)}
		},
		Started: func(j *orchestrate.Job, ds map[string]device.Info) {
			label := orchestrate.DeviceLabel(ds)
			if d, ok := ds[""]; ok && len(ds) == 1 && chosenForYou && d.Platform != device.Web && len(j.Needs) == 1 {
				passed, others := pool.PassedOver(j.Needs[0], d.Serial)
				if others > 0 || d.Serial == preferred {
					label = d.Name + " (" + d.Serial + ")"
				}
				if others > 0 && d.Serial != preferred && d.Serial != announced && !con.quiet {
					announced = d.Serial
					con.printf("%s\n", deviceChoiceNote(d, passed))
				}
			}
			con.flowStart(j, label)
		},
		Failure: func(r orchestrate.Result, drivers map[string]driver.Driver, notes map[string]string) {
			dir := flowDebugDir(debugRoot, r.Job)
			for role, n := range notes {
				logs.note(dir, role, n)
			}
			saveFailure(dir, r, drivers)
		},
		Notice: func(j *orchestrate.Job, d device.Info, msg string) {
			msg = strings.ReplaceAll(strings.TrimSpace(msg), "\n", "\n    ")
			con.printf("  %s %s (%s)\n", con.paint("33", "⚠"), msg, j.Flow.Name())
		},
		Finished: func(r orchestrate.Result) {
			dir := flowDebugDir(debugRoot, r.Job)
			if r.Status == engine.Failed {
				saveFailure(dir, r, nil)
			}
			logs.close(dir, r)
			con.flowEnd(r, dir)
			page := timelines.close(dir, r)
			pagesMu.Lock()
			pages[r.Job] = page
			pagesMu.Unlock()
			if page != "" && (r.Status == engine.Failed || !con.quiet) {
				con.printf("  timeline: %s\n", page)
			}
			for _, v := range r.Videos {
				if r.Status == engine.Failed || !con.quiet {
					con.printf("  video: %s\n", v.Path)
				}
			}
		},
	})
	for _, restore := range restores {
		restore()
	}

	var flows []report.Flow
	for _, r := range results {
		dir := flowDebugDir(debugRoot, r.Job)
		if hc := (*orchestrate.HookCrash)(nil); errors.As(r.Err, &hc) && hc.Hook == "Finished" {
			reportCrash(con, logs, dir, r)
		}
		var videos []string
		for _, v := range r.Videos {
			videos = append(videos, v.Path)
		}
		flows = append(flows, flowReport(r, dir, pages[r.Job], videos))
	}

	failed := 0
	for _, r := range results {
		if r.Status == engine.Failed || (replay && r.Status != engine.Passed) {
			failed++
		}
	}

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
		if err := os.WriteFile(out, report.HTMLIn([]report.Suite{suite}, strings.HasSuffix(strings.ToLower(a.str("format")), "detailed"), filepath.Dir(out)), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "report:", err)
		}
	}

	replaySteps, failedAt, failedDesc := 0, 0, ""
	if replay && len(flows) == 1 {
		replaySteps = len(plan.Flows[0].Commands)
		failedAt, failedDesc = failedStep(flows[0])
	}
	if jsonOut {
		fmt.Println(string(testJSON(flows, replaySteps, failedAt)))
	} else {
		con.summary(results)
		if failedAt > 0 {
			fmt.Print(replayFailure(a.pos[0], a.str("device"), failedAt, replaySteps, failedDesc, plan.Flows[0].Config.Web()))
		}
	}
	if ctx.Err() != nil {
		return 130
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func flowReport(r orchestrate.Result, dir, page string, videos []string) report.Flow {
	fr := report.Flow{Name: r.Job.Flow.Name(), File: r.Job.Flow.Path, Device: orchestrate.DeviceLabel(r.Devices),
		Duration: r.Duration, Start: r.Start, Tags: r.Job.Flow.Config.Tags, Properties: r.Job.Flow.Config.Properties,
		Timeline: page, Videos: videos}
	switch r.Status {
	case engine.Failed:
		fr.Status = "ERROR"
		fr.DebugDir = dir
		if r.Err != nil {
			fr.Error = r.Err.Error()
		}
	case engine.Skipped:
		fr.Status = "SKIPPED"
		if r.Err != nil {
			fr.Error = r.Err.Error()
		}
	default:
		fr.Status = "SUCCESS"
	}
	for _, s := range r.Steps {
		fr.Steps = append(fr.Steps, report.Step{Desc: s.Desc, Status: string(s.Status), Error: s.Err, Duration: s.Duration})
	}
	return fr
}

func testJSON(flows []report.Flow, replaySteps, failedAt int) []byte {
	replay := replaySteps > 0
	type jflow struct {
		Name       string   `json:"name"`
		File       string   `json:"file"`
		Device     string   `json:"device"`
		Status     string   `json:"status"`
		Seconds    float64  `json:"seconds"`
		Error      string   `json:"error,omitempty"`
		FailedStep int      `json:"failedStep,omitempty"`
		Steps      int      `json:"steps,omitempty"`
		DebugDir   string   `json:"debugDir,omitempty"`
		Timeline   string   `json:"timeline,omitempty"`
		Videos     []string `json:"videos,omitempty"`
	}
	var out struct {
		Passed  int     `json:"passed"`
		Failed  int     `json:"failed"`
		Skipped int     `json:"skipped"`
		Flows   []jflow `json:"flows"`
	}
	for _, f := range flows {
		switch {
		case f.Status == "ERROR" || (replay && f.Status != "SUCCESS"):
			out.Failed++
		case f.Status == "SKIPPED":
			out.Skipped++
		default:
			out.Passed++
		}
		jf := jflow{Name: f.Name, File: f.File, Device: f.Device, Status: f.Status, Seconds: f.Duration.Seconds(), Error: f.Error,
			DebugDir: f.DebugDir, Timeline: f.Timeline, Videos: f.Videos}
		if failedAt > 0 {
			jf.FailedStep, jf.Steps = failedAt, replaySteps
		}
		out.Flows = append(out.Flows, jf)
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return b
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func nameDebugDirs(jobs []*orchestrate.Job) {
	key := func(name string, j *orchestrate.Job) string { return strings.ToLower(name + shardSuffix(j)) }
	base := make([]string, len(jobs))
	groups := map[string][]int{}
	for i, j := range jobs {
		base[i] = safeDirName(j.Flow.Name())
		k := key(base[i], j)
		groups[k] = append(groups[k], i)
	}
	taken := map[string]bool{}
	for i, j := range jobs {
		if len(groups[key(base[i], j)]) == 1 {
			j.DebugName = base[i]
			taken[key(base[i], j)] = true
		}
	}
	for i, j := range jobs {
		g := groups[key(base[i], j)]
		if len(g) == 1 || g[0] != i {
			continue
		}
		var dirs []string
		for _, idx := range g {
			if p := jobs[idx].Flow.Path; p != "" {
				dirs = append(dirs, filepath.Dir(p))
			}
		}
		common := commonDir(dirs)
		quals := make([][]string, len(g))
		for n, idx := range g {
			quals[n] = debugQualifiers(jobs[idx].Flow.Path, common)
		}
		for n, idx := range g {
			name := ""
			for level := 0; level < len(quals[n]) && name == ""; level++ {
				q := quals[n][level]
				if q == "" {
					continue
				}
				unique := true
				for m := range g {
					if m != n && level < len(quals[m]) && strings.EqualFold(quals[m][level], q) {
						unique = false
					}
				}
				if full := base[idx] + "-" + q; unique && !taken[key(full, jobs[idx])] {
					name = full
				}
			}
			for k := n + 1; name == ""; k++ {
				if full := fmt.Sprintf("%s-%d", base[idx], k); !taken[key(full, jobs[idx])] {
					name = full
				}
			}
			jobs[idx].DebugName = name
			taken[key(name, jobs[idx])] = true
		}
	}
}

func debugQualifiers(path, common string) []string {
	if path == "" {
		return nil
	}
	clean := func(s string) string {
		if s == "." || s == ".." || s == string(filepath.Separator) {
			return ""
		}
		return strings.Trim(unsafeName.ReplaceAllString(s, "_"), "_")
	}
	rel := func(p string) string {
		if r, err := filepath.Rel(common, p); err == nil && common != "" {
			return clean(r)
		}
		return clean(p)
	}
	dir := filepath.Dir(path)
	noExt := strings.TrimSuffix(path, filepath.Ext(path))
	return []string{clean(filepath.Base(dir)), rel(dir), clean(filepath.Base(noExt)), rel(noExt)}
}

func commonDir(dirs []string) string {
	if len(dirs) == 0 {
		return ""
	}
	c := dirs[0]
	for _, d := range dirs[1:] {
		for c != filepath.Dir(c) {
			if r, err := filepath.Rel(c, d); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
				break
			}
			c = filepath.Dir(c)
		}
	}
	return c
}

func shardSuffix(j *orchestrate.Job) string {
	if j.ShardID > 0 {
		return fmt.Sprintf("-shard-%d", j.ShardID)
	}
	return ""
}

func safeDirName(s string) string {
	name := unsafeName.ReplaceAllString(s, "_")
	if name == "" || name == "." || name == ".." {
		name = "_" + name
	}
	return name
}

func flowDebugDir(root string, j *orchestrate.Job) string {
	name := j.DebugName
	if name == "" {
		name = safeDirName(j.Flow.Name())
	}
	name += shardSuffix(j)
	if j.Attempt > 0 {
		name += fmt.Sprintf("-retry-%d", j.Attempt)
	}
	return filepath.Join(root, name)
}

func consoleWriter(jsonOut bool) io.Writer {
	if jsonOut {
		return os.Stderr
	}
	return os.Stdout
}

var failureArtifactTimeout = 10 * time.Second

func disableAnimations(ctx context.Context, drv driver.Driver) func() {
	type animator interface {
		DisableAnimations(context.Context) (func(), error)
	}
	an, ok := drv.(animator)
	if !ok {
		return nil
	}
	restore, _ := an.DisableAnimations(ctx)
	return restore
}

func saveFailure(dir string, r orchestrate.Result, drivers map[string]driver.Driver) {
	os.MkdirAll(dir, 0o755)
	if r.Err != nil {
		os.WriteFile(filepath.Join(dir, "error.txt"), []byte(r.Err.Error()+"\n"), 0o644)
	}
	for role, drv := range drivers {
		suffix := ""
		if role != "" {
			suffix = "-" + role
		}
		c, cancel := context.WithTimeout(context.Background(), failureArtifactTimeout)
		if img, err := drv.Screenshot(c, 1, "png"); err == nil {
			os.WriteFile(filepath.Join(dir, "screenshot"+suffix+".png"), img, 0o644)
		}
		cancel()
		c, cancel = context.WithTimeout(context.Background(), failureArtifactTimeout)
		if s, err := drv.Screen(c); err == nil {
			os.WriteFile(filepath.Join(dir, "screen"+suffix+".txt"), []byte(look.Build(s, look.NewRefs()).Text()), 0o644)
			if b, err := json.MarshalIndent(hierarchyJSON(s), "", " "); err == nil {
				os.WriteFile(filepath.Join(dir, "hierarchy"+suffix+".json"), b, 0o644)
			}
		}
		cancel()
		if logs, ok := drv.(driver.AppLogReader); ok && r.Job != nil && r.Job.Flow.Config.AppID != "" {
			appID := r.Job.Flow.Config.AppID
			c, cancel = context.WithTimeout(context.Background(), failureArtifactTimeout)
			if log, err := logs.AppLog(c, appID, time.Since(r.Start)); err == nil {
				if text := log.Text(appID); text != "" {
					os.WriteFile(filepath.Join(dir, "app"+suffix+".log"), []byte(text), 0o644)
				}
			}
			cancel()
		}
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

type flowLogs struct {
	mu    sync.Mutex
	files map[string]*os.File
	trace map[string]*engine.Trace
}

func newFlowLogs() *flowLogs {
	return &flowLogs{files: map[string]*os.File{}, trace: map[string]*engine.Trace{}}
}

func (l *flowLogs) open(dir string, j *orchestrate.Job, devs map[string]device.Info) *engine.Trace {
	var w []io.Writer
	if os.Getenv("TB_DEBUG") != "" {
		w = append(w, os.Stderr)
	}
	var f *os.File
	if err := os.MkdirAll(dir, 0o755); err == nil {
		f, _ = os.Create(filepath.Join(dir, "log.txt"))
	}
	if f != nil {
		w = append(w, f)
	}
	if len(w) == 0 {
		return nil
	}
	t := engine.NewTrace(io.MultiWriter(w...))
	t.Printf("", 0, "tb %s: %q (%s)", version, j.Flow.Name(), j.Flow.Path)
	roles := make([]string, 0, len(devs))
	for role := range devs {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		d := devs[role]
		who := "device"
		if role != "" {
			who = "role " + role
		}
		t.Printf("", 0, "%s: %s %s (%s)", who, d.Platform, d.Name, d.Serial)
	}
	if j.Attempt > 0 {
		t.Printf("", 0, "retry %d", j.Attempt)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if old := l.files[dir]; old != nil {
		old.Close()
	}
	l.files[dir], l.trace[dir] = f, t
	return t
}

func (l *flowLogs) note(dir, role, msg string) {
	l.mu.Lock()
	t := l.trace[dir]
	l.mu.Unlock()
	if t != nil {
		t.Printf(role, 0, "%s", msg)
	}
}

func (l *flowLogs) close(dir string, r orchestrate.Result) {
	l.mu.Lock()
	f, t := l.files[dir], l.trace[dir]
	delete(l.files, dir)
	delete(l.trace, dir)
	l.mu.Unlock()
	if t == nil {
		if r.Status != engine.Failed || r.Err == nil {
			return
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		nf, err := os.Create(filepath.Join(dir, "log.txt"))
		if err != nil {
			return
		}
		defer nf.Close()
		t = engine.NewTrace(nf)
		t.Printf("", 0, "tb %s: %q (%s)", version, r.Job.Flow.Name(), r.Job.Flow.Path)
	}
	switch {
	case r.Status == engine.Failed && r.Err != nil:
		t.Printf("", 0, "FAILED in %s: %v", r.Duration.Round(time.Millisecond), r.Err)
		if shots, _ := filepath.Glob(filepath.Join(dir, "screen*")); len(shots) > 0 {
			t.Printf("", 0, "the screen at the failure: screen*.txt, screenshot*.png, hierarchy*.json in this folder")
		}
	default:
		t.Printf("", 0, "%s in %s", strings.ToUpper(string(r.Status)), r.Duration.Round(time.Millisecond))
	}
	if f != nil {
		f.Close()
	}
}

func (l *flowLogs) crashed(dir string, r orchestrate.Result) {
	l.mu.Lock()
	f, t := l.files[dir], l.trace[dir]
	delete(l.files, dir)
	delete(l.trace, dir)
	l.mu.Unlock()
	if t == nil {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		nf, err := os.OpenFile(filepath.Join(dir, "log.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		f, t = nf, engine.NewTrace(nf)
	}
	t.Printf("", 0, "FAILED in %s: %v", r.Duration.Round(time.Millisecond), r.Err)
	if f != nil {
		f.Close()
	}
}

func reportCrash(con *console, logs *flowLogs, dir string, r orchestrate.Result) {
	defer func() {
		if p := recover(); p != nil {
			fmt.Fprintf(os.Stderr, "✗ %s failed: %v\n  debug: %s\n", r.Job.Flow.Name(), r.Err, dir)
		}
	}()
	if os.MkdirAll(dir, 0o755) == nil {
		os.WriteFile(filepath.Join(dir, "error.txt"), []byte(r.Err.Error()+"\n"), 0o644)
	}
	logs.crashed(dir, r)
	con.flowEnd(r, dir)
}

const maxAnnotateStep = time.Minute

func parseAnnotateStep(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	var d time.Duration
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		d = time.Duration(f * float64(time.Second))
	} else if d, err = time.ParseDuration(s); err != nil {
		return 0, fmt.Errorf("not a time: %q (e.g. 0.5s, 500ms, 3s, or seconds: 2)", s)
	}
	if d < 0 || d > maxAnnotateStep {
		return 0, fmt.Errorf("%q: from 0 to %s", s, maxAnnotateStep)
	}
	if d == 0 {
		return -1, nil
	}
	return d, nil
}

func parseWait(s string) (time.Duration, error) {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 0 {
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil || d < 0 {
		return 0, fmt.Errorf("not a duration: %q (e.g. 5m, 90s)", s)
	}
	return d, nil
}

func claimDir(parent, name string) string {
	os.MkdirAll(parent, 0o755)
	for i := 1; i < 1000; i++ {
		dir := filepath.Join(parent, name)
		if i > 1 {
			dir = fmt.Sprintf("%s-%d", dir, i)
		}
		if err := os.Mkdir(dir, 0o755); err == nil || !os.IsExist(err) {
			return dir
		}
	}
	return filepath.Join(parent, name)
}

func reclaimFromOwnSession(ctx context.Context, serial string) orchestrate.Reclaimed {
	cl := daemon.Dial()
	if cl == nil {
		if daemon.Running() {
			return orchestrate.Reclaimed{Unknown: true}
		}
		return orchestrate.Reclaimed{}
	}
	defer cl.Close()
	cl.SetDeadline(time.Now().Add(reclaimWait))
	stop := context.AfterFunc(ctx, func() { cl.SetDeadline(time.Now()) })
	defer stop()
	args, _ := json.Marshal(session.YieldArgs{Serial: serial, Owner: strings.TrimSpace(os.Getenv("TB_SESSION_OWNER"))})
	resp, err := cl.Call(daemon.Request{Cmd: "yield", Args: args}, nil)
	var y session.Yielded
	if err != nil || !resp.OK || json.Unmarshal(resp.JSON, &y) != nil {
		return orchestrate.Reclaimed{Unknown: true}
	}
	if y.Yielded {
		fmt.Fprintf(os.Stderr, "note: closed your tb session %s for this run; tb look or tb do on it opens it again\n", y.Label)
	}
	return orchestrate.Reclaimed{OK: y.Yielded, Busy: y.Busy, Label: y.Label}
}

const reclaimWait = 15 * time.Second

func hasMultiDeviceFlow(plan *workspace.Plan) bool {
	for _, f := range plan.Flows {
		if len(f.Config.Devices) > 0 {
			return true
		}
	}
	return false
}

func preferFirst(devs []device.Info, serial string) []device.Info {
	out := make([]device.Info, 0, len(devs))
	for _, d := range devs {
		if d.Serial == serial {
			out = append(out, d)
		}
	}
	for _, d := range devs {
		if d.Serial != serial {
			out = append(out, d)
		}
	}
	return out
}

func flowsNamePlatform(plan *workspace.Plan) bool {
	for _, f := range plan.Flows {
		if len(f.Config.Devices) == 0 && !f.Config.Web() {
			return false
		}
	}
	return true
}

var sessionDevice = func(ctx context.Context, name string) string {
	cl := daemon.Dial()
	if cl == nil {
		return ""
	}
	defer cl.Close()
	cl.SetDeadline(time.Now().Add(10 * time.Second))
	stop := context.AfterFunc(ctx, func() { cl.SetDeadline(time.Now()) })
	defer stop()
	args, _ := json.Marshal(map[string]string{"owner": strings.TrimSpace(os.Getenv("TB_SESSION_OWNER"))})
	resp, err := cl.Call(daemon.Request{Cmd: "devices", Args: args}, nil)
	var devs []struct {
		Serial, Session string
		Others          bool
	}
	if err != nil || !resp.OK || json.Unmarshal(resp.JSON, &devs) != nil {
		return ""
	}
	for _, d := range devs {
		if d.Session != "" && d.Session == name && !d.Others {
			return d.Serial
		}
	}
	return ""
}

func needsPhone(plan *workspace.Plan) bool {
	for _, f := range plan.Flows {
		if !f.Config.Web() {
			return true
		}
	}
	return false
}

func phones(devs []device.Info) int {
	n := 0
	for _, d := range devs {
		if d.Platform != device.Web {
			n++
		}
	}
	return n
}

func deviceChoiceNote(d device.Info, passed []string) string {
	s := fmt.Sprintf("note: no device named, so this run uses %s (%s)", d.Name, d.Serial)
	if len(passed) > 0 {
		s += "; not " + strings.Join(passed, ", not ")
	}
	return s + ". Pin one with --device or TB_DEVICE."
}
