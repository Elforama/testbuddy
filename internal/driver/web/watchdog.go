package web

// How tb and the browser's watchdog (watchdog_unix.go, issue #81) recognise each other: a
// watchdog runs only when started with watchdogArg and watchdogEnv=watchdogProto, so a user who
// exports watchdogEnv does not turn tb into one, and it answers watchdogReady and Chrome's pid,
// so tb knows the executable it started is a watchdog of this protocol and not another tb.
const (
	watchdogArg   = "__tb_browser_watchdog"
	watchdogEnv   = "TB_WEB_BROWSER_WATCHDOG"
	watchdogProto = "1"
	watchdogReady = "tb-browser-watchdog-1 started"
)
