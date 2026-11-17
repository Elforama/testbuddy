// The probe's gesture trace (issue #337): what the app made of a gesture tb sent, for a gesture
// that reached the app (its "touches completed" came on time) and had no effect. Off unless the
// app is launched with TB_GESTURE_TRACE set to a file (the host does so in CI mode, or with
// TB_IOS_GESTURE_TRACE=1; driver/ios/apps.go). When off, the probe installs nothing for it.
//
// One gesture runs from a touch-down with no other touch down until 1.5 s after its last lift,
// or until the next touch-down, unless that comes within 500 ms of the lift (a double tap is one
// gesture); its entries go in a ring buffer (the oldest are dropped past
// TB_TRACE_CAP, and the header says how many), and the gesture is written as one block, off the
// main thread. All inline, like TBTouchGuard.h: the probe includes it, and the Swift package
// tests it (CoreTests/GestureTraceTests.swift).
#ifndef TB_GESTURE_TRACE_H
#define TB_GESTURE_TRACE_H

#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <math.h>
#include <time.h>

#define TB_TRACE_CAP 256
#define TB_TRACE_STALL_NS 50000000ull   // a main-thread turn longer than this is a stall
#define TB_TRACE_JOIN_NS 500000000ull   // a touch-down this soon after a lift continues the gesture
#define TB_TRACE_TAIL_NS 1500000000ull  // a gesture ends this long after its last lift...
#define TB_TRACE_TAIL_MAX_NS 5000000000ull // ...or after this, a scroll view still moving
#define TB_TRACE_LINE 400 // the longest line
#define TB_TRACE_MOVE_EVERY_NS 100000000ull // moves are recorded at most this often

typedef enum {
    TBTraceTouch = 1,  // a touch phase sendEvent saw: a = phase, id = touch, v = (x, y, lag ms, taps)
    TBTraceScroll,     // a scroll view: a = moment, flags, v = (offset x, y, velocity x, y)
    TBTracePageTarget, // a paging scroll view at release: v = (target x, y, offset at lift x, y)
    TBTraceRecognizer, // a gesture recognizer's state change: a = from, b = to, flags = on the touch;
                       // to failed or cancelled, extra = the ones that recognized since the touch-down
    TBTraceStall,      // a main-thread turn over 50 ms: v0 = its length in ms
    TBTraceSees,       // a recognizer that sees the touch, first time: extra = what it is
} TBTraceKind;

// Scroll view moments (TBTraceScroll's a).
typedef enum { TBMomentDown = 0, TBMomentLift, TBMomentRelease, TBMomentSettled } TBTraceMoment;

// Scroll view flags (TBTraceScroll's flags).
enum {
    TBScrollTracking = 1, TBScrollDragging = 2, TBScrollDecelerating = 4, TBScrollPaging = 8,
    TBScrollPanBegan = 16, TBScrollPanFailed = 32,
};

typedef struct {
    uint64_t t;      // ns, the probe's clock (mach absolute time in ns)
    uint8_t kind;    // TBTraceKind
    uint8_t a, b;
    uint8_t flags;
    int32_t id;      // the touch, scroll view or recognizer's number in this gesture
    double v[4];
    char name[64];   // a class name
    char extra[128];
} TBTraceEntry;

typedef struct {
    TBTraceEntry *e;
    uint32_t cap;
    uint32_t head;   // the oldest entry
    uint32_t count;
    uint32_t dropped;
} TBTraceRing;

// Whether the trace is on, from TB_GESTURE_TRACE: a file to append to. Unset, empty or "0": off.
static inline bool tbTraceEnabled(const char *env) {
    return env && *env && strcmp(env, "0") != 0;
}

static inline void tbTraceInit(TBTraceRing *r, TBTraceEntry *storage, uint32_t cap) {
    r->e = storage; r->cap = cap; r->head = 0; r->count = 0; r->dropped = 0;
}

static inline void tbTraceReset(TBTraceRing *r) { r->head = 0; r->count = 0; r->dropped = 0; }

// Adds an entry; when full, the oldest is dropped (and counted).
static inline void tbTracePush(TBTraceRing *r, const TBTraceEntry *x) {
    if (!r->cap) return;
    if (r->count < r->cap) {
        r->e[(r->head + r->count) % r->cap] = *x;
        r->count++;
    } else {
        r->e[r->head] = *x;
        r->head = (r->head + 1) % r->cap;
        r->dropped++;
    }
}

// The i-th entry, oldest first.
static inline const TBTraceEntry *tbTraceAt(const TBTraceRing *r, uint32_t i) {
    return i < r->count ? &r->e[(r->head + i) % r->cap] : NULL;
}

static inline void tbTraceCopy(char *dst, size_t len, const char *src) {
    if (!len) return;
    snprintf(dst, len, "%s", src ? src : "");
}

static inline const char *tbTracePhaseName(int p) {
    // UITouchPhase
    static const char *names[] = {"began", "moved", "stationary", "ended", "cancelled"};
    return p >= 0 && p < 5 ? names[p] : "phase?";
}

static inline const char *tbTraceStateName(int s) {
    // UIGestureRecognizerState
    static const char *names[] = {"possible", "began", "changed", "ended", "cancelled", "failed"};
    return s >= 0 && s < 6 ? names[s] : "state?";
}

static inline const char *tbTraceMomentName(int m) {
    static const char *names[] = {"down", "lift", "release", "settled"};
    return m >= 0 && m < 4 ? names[m] : "moment?";
}

// One entry as a line (no newline), its time from the gesture's start. The length written.
static inline int tbTraceFormat(const TBTraceEntry *x, uint64_t start, char *buf, size_t len) {
    double ms = x->t >= start ? (double)(x->t - start) / 1e6 : -(double)(start - x->t) / 1e6;
    int n = snprintf(buf, len, "%+9.1f ms  ", ms);
    if (n < 0 || (size_t)n >= len) return n;
    char *p = buf + n;
    size_t left = len - n;
    int m = 0;
    switch (x->kind) {
    case TBTraceTouch:
        m = snprintf(p, left, "touch %d %s at (%.1f,%.1f) taps %d, %.1f ms after the system's event time",
                     x->id, tbTracePhaseName(x->a), x->v[0], x->v[1], (int)x->v[3], x->v[2]);
        break;
    case TBTraceScroll:
        m = snprintf(p, left, "scroll #%d %s %s: offset (%.1f,%.1f) velocity (%.0f,%.0f)%s%s%s%s%s%s",
                     x->id, x->name, tbTraceMomentName(x->a), x->v[0], x->v[1], x->v[2], x->v[3],
                     x->flags & TBScrollPaging ? " paging" : "",
                     x->flags & TBScrollTracking ? " tracking" : "",
                     x->flags & TBScrollDragging ? " dragging" : "",
                     x->flags & TBScrollDecelerating ? " decelerating" : "",
                     x->flags & TBScrollPanBegan ? " pan-began" : "",
                     x->flags & TBScrollPanFailed ? " pan-failed" : "");
        break;
    case TBTracePageTarget:
        if (isfinite(x->v[0]))
            m = snprintf(p, left, "page target #%d %s: offset (%.1f,%.1f), at lift (%.1f,%.1f)",
                         x->id, x->name, x->v[0], x->v[1], x->v[2], x->v[3]);
        else
            m = snprintf(p, left, "page target #%d %s: unknown, at lift (%.1f,%.1f)",
                         x->id, x->name, x->v[2], x->v[3]);
        break;
    case TBTraceSees:
        m = snprintf(p, left, "recognizer #%d %s sees the touch%s%s", x->id, x->name,
                     x->extra[0] ? " " : "", x->extra);
        break;
    case TBTraceRecognizer:
        m = snprintf(p, left, "recognizer #%d %s: %s -> %s%s%s%s", x->id, x->name,
                     tbTraceStateName(x->a), tbTraceStateName(x->b), x->flags ? "" : " (not on the touch)",
                     x->extra[0] ? ", after " : "", x->extra);
        break;
    case TBTraceStall:
        m = snprintf(p, left, "main thread stalled %.1f ms", x->v[0]);
        break;
    default:
        m = snprintf(p, left, "entry %d", x->kind);
    }
    return m < 0 ? m : n + m;
}

// The gesture's first line: its number, the process and app, its wall-clock start (seconds since
// 1970 with ms, to set beside the agent's and the simulator's logs), and what was dropped.
static inline int tbTraceHeader(int gesture, int pid, const char *app, double wallStart,
                                const TBTraceRing *r, char *buf, size_t len) {
    time_t secs = (time_t)wallStart;
    struct tm tm;
    gmtime_r(&secs, &tm);
    int ms = (int)((wallStart - (double)secs) * 1000);
    if (ms > 999) ms = 999;
    char dropped[48] = "";
    if (r->dropped) snprintf(dropped, sizeof dropped, " (%u earlier dropped)", r->dropped);
    return snprintf(buf, len, "gesture %d, pid %d %s, %04d-%02d-%02d %02d:%02d:%02d.%03d UTC, %u entries%s",
                    gesture, pid, app ? app : "", tm.tm_year + 1900, tm.tm_mon + 1, tm.tm_mday,
                    tm.tm_hour, tm.tm_min, tm.tm_sec, ms, r->count, dropped);
}

// The whole gesture: its header, then each entry on its own line, indented, times from start.
// Stops at a whole line when buf is full. The length written (at most len - 1).
static inline size_t tbTraceFormatGesture(int gesture, int pid, const char *app, double wallStart,
                                          uint64_t start, const TBTraceRing *r, char *buf, size_t len) {
    if (!len) return 0;
    buf[0] = 0;
    size_t used = 0;
    char line[TB_TRACE_LINE];
    int n = tbTraceHeader(gesture, pid, app, wallStart, r, line, sizeof line);
    if (n < 0) return 0;
    for (uint32_t i = 0; ; i++) {
        size_t l = strlen(line);
        if (used + l + 2 >= len) break;
        memcpy(buf + used, line, l);
        used += l;
        buf[used++] = '\n';
        buf[used] = 0;
        const TBTraceEntry *x = tbTraceAt(r, i);
        if (!x) break;
        line[0] = ' '; line[1] = ' ';
        if (tbTraceFormat(x, start, line + 2, sizeof line - 2) < 0) line[2] = 0;
    }
    return used;
}

#endif
