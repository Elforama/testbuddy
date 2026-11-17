// The probe's decision at the start of a guarded touch (TBProbe.m's sendEvent: hook): let it
// reach the app, or withhold it. Plain C with no UIKit, so `swift test` covers it (CoreTests).
#ifndef TB_TOUCH_GUARD_H
#define TB_TOUCH_GUARD_H

#include <math.h>
#include <stdbool.h>
#include <stdint.h>

typedef struct {
    double x, y, w, h;
} TBFrame;

// A frame in one 64-bit notify state, so the probe reads it whole: x and y as signed, width and
// height as unsigned 16-bit counts of quarter points. 0 means none (a packed frame is never 0: its
// size is at least a quarter point). TouchGuard.pack is the agent's side of it.
static inline uint64_t tbPackFrame(TBFrame f) {
    double q[4] = {round(f.x * 4), round(f.y * 4), round(f.w * 4), round(f.h * 4)};
    if (!(q[0] >= INT16_MIN && q[0] <= INT16_MAX && q[1] >= INT16_MIN && q[1] <= INT16_MAX &&
          q[2] >= 1 && q[2] <= UINT16_MAX && q[3] >= 1 && q[3] <= UINT16_MAX)) return 0;
    return (uint64_t)(uint16_t)(int16_t)q[0] << 48 | (uint64_t)(uint16_t)(int16_t)q[1] << 32 |
           (uint64_t)(uint16_t)q[2] << 16 | (uint64_t)(uint16_t)q[3];
}

static inline TBFrame tbUnpackFrame(uint64_t v) {
    TBFrame f = {(int16_t)(uint16_t)(v >> 48) / 4.0, (int16_t)(uint16_t)(v >> 32) / 4.0,
                 (uint16_t)(v >> 16) / 4.0, (uint16_t)v / 4.0};
    return f;
}

// Why a touch is withheld (tbWithholdReason).
typedef enum {
    TBWithheldNone = 0,      // not withheld: it reaches the app
    TBWithheldOffTarget = 1, // the point is off the target
    TBWithheldMoved = 2,     // the target moved since the agent read it (issue #103)
} TBWithheld;

// tbWithholdReason: whether, and why, a touch going down at (px, py) must not reach the app.
// live is the target's accessibility frame now; armed is the frame the agent read just before
// it sent the touch (packed; 0: none).
static inline TBWithheld tbWithholdReason(TBFrame live, double px, double py, uint64_t armed) {
    // Off the target (with a point of slack): it is not where the tap was aimed.
    if (!(px >= live.x - 1 && px < live.x + live.w + 1 && py >= live.y - 1 && py < live.y + live.h + 1)) return TBWithheldOffTarget;
    if (!armed) return TBWithheldNone;
    // Moved since the agent read it (issue #103): a move that starts between that read and the
    // touch. The point can still be on its frame, but SwiftUI lays a moving view out where the
    // move ends, and its button does not take the touch. Withheld, the host finds the target
    // again once it is still (in its CI mode; elsewhere the tap fails, saying so). Its middle,
    // not its edges: a button pulsing around its middle changes size, not place, and stays
    // tappable.
    TBFrame read = tbUnpackFrame(armed);
    bool moved = fabs((live.x + live.w / 2) - (read.x + read.w / 2)) > 0.5 || fabs((live.y + live.h / 2) - (read.y + read.h / 2)) > 0.5;
    return moved ? TBWithheldMoved : TBWithheldNone;
}

// tbWithholdTouch: whether a touch going down at (px, py) must not reach the app.
static inline bool tbWithholdTouch(TBFrame live, double px, double py, uint64_t armed) {
    return tbWithholdReason(live, px, py, armed) != TBWithheldNone;
}

// The probe's rejection of an armed touch (its rejected state): the touch's nonce, with this
// flag when the target moved (TBWithheldMoved) rather than the point being off it. A nonce never
// uses the flag's bit (TouchGuard.nonce). TouchGuard.rejection reads it.
static const uint64_t TBRejectedMoved = UINT64_C(1) << 63;

static inline uint64_t tbRejection(uint64_t nonce, bool moved) {
    return nonce | (moved ? TBRejectedMoved : 0);
}

// The probe's receipt for an armed touch, published once it has ended: its nonce, with flags in
// the top bits (a nonce never uses them: TouchGuard.nonce). TouchGuard.receipt reads it.
static const uint64_t TBReceiptTargeted = UINT64_C(1) << 63; // it began on the named target's live frame
static const uint64_t TBReceiptCancelled = UINT64_C(1) << 62;

// tbTouchReceipt: the receipt for a touch that has ended. targeted: it began on the target's
// frame; cancelled: it ended cancelled.
//
// A touch that ends cancelled (UIKit hands it to the app with UITouchPhaseCancelled: the
// system took it back, for one because its digitizer went away before it lifted) is no tap:
// a button acts when a touch ends on it, not when one is cancelled. Issue #131: one went down
// on a dialog's button (the press showed, so the screen changed), was cancelled, and the tap
// counted as done. Its receipt says cancelled, never targeted, so the host taps again.
static inline uint64_t tbTouchReceipt(uint64_t nonce, bool targeted, bool cancelled) {
    if (cancelled) return nonce | TBReceiptCancelled;
    return nonce | (targeted ? TBReceiptTargeted : 0);
}

// Issue #324: a watched touch, the tap on a button tb found by its text, not its id. The agent
// arms it as a named one (TouchGuard.nonce), with this in place of the id's hash, and the
// target's bounds as tb read them in place of the live frame. The probe never withholds it: its
// receipt only says, with TBReceiptTargeted, whether the touch went to the view with those
// bounds or one inside it (tbViewIsTarget). A probe older than this takes it for an id no view has: it finds no
// target, withholds nothing and reports the touch untargeted, which tells the host nothing.
static const uint32_t TBWatchedTouch = 0x5ca1ab1eu;

// tbViewIsTarget: whether a view the touch went to (UIKit's hit-test view, or one of its
// superviews), its frame on screen, is the target: its frame is the target's bounds, each edge
// within a point and a half (frames are fractional, tb's bounds whole points). A touch that
// went to the target's own view, or to a view inside it (its label), reached it, not something
// over it: a veil hidden from accessibility is not the button it covers, and neither is a view
// inside the veil that happens to lie inside the button (its spinner, over a button in the
// middle of the screen), so being inside the bounds is not enough. A target as big as half the
// screen tells nothing (a veil over the whole screen would match a button that size), nor does
// an empty frame. SwiftUI's buttons and Flutter's have no view of their own: their touches go
// to a hosting view, which never matches.
static inline bool tbViewIsTarget(TBFrame view, TBFrame target, double screenW, double screenH) {
    if (!(view.w > 0 && view.h > 0 && target.w > 0 && target.h > 0)) return false;
    if (!(isfinite(view.x) && isfinite(view.y) && isfinite(view.w) && isfinite(view.h))) return false;
    if (!(screenW > 0 && screenH > 0) || target.w * target.h * 2 > screenW * screenH) return false;
    const double slack = 1.5;
    return fabs(view.x - target.x) <= slack && fabs(view.y - target.y) <= slack &&
           fabs(view.x + view.w - (target.x + target.w)) <= slack && fabs(view.y + view.h - (target.y + target.h)) <= slack;
}

// TBChainView: one view on the way up from a touch's hit-test view (TBProbe.m tbTouchOnView):
// its frame on screen; element: it is an accessibility element (what XCUI lists, the button tb
// found); concealed: it is hidden, all but transparent (alpha < 0.01), or hides its elements from
// accessibility (accessibilityElementsHidden).
typedef struct {
    TBFrame frame;
    bool element;
    bool concealed;
} TBChainView;

// tbViewInTarget: whether a view's frame on screen lies inside the target's bounds (with
// tbViewIsTarget's slack), for a target that can tell anything (tbViewIsTarget's size rule).
static inline bool tbViewInTarget(TBFrame view, TBFrame target, double screenW, double screenH) {
    if (!(view.w > 0 && view.h > 0 && target.w > 0 && target.h > 0)) return false;
    if (!(isfinite(view.x) && isfinite(view.y) && isfinite(view.w) && isfinite(view.h))) return false;
    if (!(screenW > 0 && screenH > 0) || target.w * target.h * 2 > screenW * screenH) return false;
    const double slack = 1.5;
    return view.x >= target.x - slack && view.y >= target.y - slack &&
           view.x + view.w <= target.x + target.w + slack && view.y + view.h <= target.y + target.h + slack;
}

// tbTouchReachedTarget (issue #324): whether a touch whose hit-test view and its superviews, in
// that order, are chain[0..n) reached the target. Only the button itself says so: an
// accessibility element with the target's bounds (tbViewIsTarget). Every view from the hit-test
// view up to it must lie inside the target (a label inside the button, the button's own
// subviews), and none may be concealed: a veil hidden from accessibility is concealed, or is
// not an element and leads up to no element with the button's bounds. So a veil inside a
// container the button's size (the container is no element), an overlay over the button the
// button's size (its superview is bigger), or a view outside the target on the way up all say
// no, and the host waits for the veil as before. No element with the target's bounds on the way
// (a SwiftUI or Flutter button, whose touch goes to a hosting view): no as well.
static inline bool tbTouchReachedTarget(const TBChainView *chain, int n, TBFrame target, double screenW, double screenH) {
    for (int i = 0; i < n; ++i) {
        const TBChainView v = chain[i];
        if (v.concealed) return false;
        if (!tbViewInTarget(v.frame, target, screenW, screenH)) return false;
        if (v.element && tbViewIsTarget(v.frame, target, screenW, screenH)) return true;
    }
    return false;
}

// The app's phase, which the probe publishes for the agent (issue #290): the state of
// dev.testbuddy.lifecycle.<pid>. AppPhase (InputReady.swift) reads it; the values match.
typedef enum {
    TBPhaseUnknown = 0,
    TBPhaseActive = 1,     // its window takes input as laid out
    TBPhaseEntering = 2,   // launched or coming back from the background, not active yet
    TBPhaseInactive = 3,   // in front, inactive: an alert, Control Center, the app switcher
    TBPhaseBackground = 4,
} TBPhase;

// The UIApplication notifications the probe follows.
typedef enum {
    TBLifeWillEnterForeground = 0,
    TBLifeDidBecomeActive = 1,
    TBLifeWillResignActive = 2,
    TBLifeDidEnterBackground = 3,
} TBLifeEvent;

// tbNextPhase: the phase after a notification. A process starts entering (TBPhaseEntering at
// load): its first activation ends the opening animation, as a return's does. Until
// "did become active", SpringBoard is still animating the app's window in, scaled, and a touch
// lands where the scaled window is: entering holds input. A resign while entering, should
// UIKit post one, leaves it inactive. UIKit posts none to an app that never became active (one
// launched under a system alert stays entering): the agent stops waiting on such an app once
// it is no longer in front, as SpringBoard is while its alert is up (InputReady.swift).
static inline TBPhase tbNextPhase(TBPhase now, TBLifeEvent e) {
    switch (e) {
    case TBLifeWillEnterForeground: return TBPhaseEntering;
    case TBLifeDidBecomeActive: return TBPhaseActive;
    case TBLifeWillResignActive: return now == TBPhaseBackground ? now : TBPhaseInactive;
    case TBLifeDidEnterBackground: return TBPhaseBackground;
    }
    return now;
}


// Restless sources: the probe's bookkeeping behind the restless bit (TBProbe.m, whose header
// comment says what a restless source is). A source is a layer (its address) or a band (an odd
// key: the layers of one class at the same height in the same superlayer). The probe holds
// tbLock around every call; plain C, so `swift test` drives the same calls the probe makes
// (RestlessTurnTests).
#define TB_TURN_MAX 128
#define TB_FRAME_GAP_NS 70000000ull               // the longest gap between frames of an animation
#define TB_RESTLESS_GAP_NS 400000000ull           // a longer gap ends a source's run of changes
#define TB_RESTLESS_AFTER_NS 500000000ull         // a run this long is restless
#define TB_FRAMES_RESTLESS_AFTER_NS 1000000000ull // ... or, frame by frame, this long
#define TB_WOKE_MIN_NS 150000000ull               // still for longer than this ...
#define TB_WOKE_RHYTHMS 3                         // ... and than this many of its usual gaps
#define TB_CROWDED_TURN 16                        // a turn this many layers change in is crowded

typedef struct {
    uintptr_t key, band;
    uint64_t since, last, maxGap; // the run of changes: its start, last change, longest gap
    uint64_t interval;            // the source's usual gap between changes (0: not known yet)
    bool restless;                // found restless: the layer is tagged on its next change
} TBSource;

typedef struct {
    TBSource *table;          // `size` slots, a power of two, zeroed
    uint32_t size;
    uint64_t framesRestlessNs; // TB_FRAMES_RESTLESS_AFTER_NS, scaled with the app's animation speed
    uintptr_t *tagged;        // TB_TURN_MAX slots: this turn's tagged layers
    int taggedCount;
    bool touchEnded;          // this turn delivered the end of a touch to the app
} TBSources;

// The slot of source k, or NULL.
static inline TBSource *tbSrcFind(TBSources *s, uintptr_t k) {
    size_t h = (k >> 4) * 2654435761u;
    for (int j = 0; j < 8; j++) {
        TBSource *e = &s->table[(h + j) & (s->size - 1)];
        if (e->key == k) return e;
    }
    return NULL;
}

// A gap of a source's, into its rhythm.
static inline void tbSrcLearn(TBSource *e, uint64_t gap) {
    if (gap > 0) e->interval = e->interval ? (3 * e->interval + gap) / 4 : gap;
}

// Records a change of source k now: its slot (the least recently changed one in its probe
// window is reused). Call it once per source and turn. *gap: the time since its last change
// (0 for a source not known), for its rhythm (tbSrcLearn), which the caller decides on.
static inline TBSource *tbSrcTouchIn(TBSources *s, uintptr_t k, uint64_t now, uint64_t *gapOut) {
    *gapOut = 0;
    size_t h = (k >> 4) * 2654435761u;
    TBSource *slot = NULL, *oldest = NULL;
    for (int j = 0; j < 8; j++) {
        TBSource *e = &s->table[(h + j) & (s->size - 1)];
        if (e->key == k) { slot = e; break; }
        if (!oldest || e->last < oldest->last) oldest = e;
    }
    if (!slot) {
        slot = oldest;
        *slot = (TBSource){.key = k, .since = now, .last = now};
        return slot;
    }
    uint64_t gap = now > slot->last ? now - slot->last : 0;
    if (gap > TB_RESTLESS_GAP_NS) { slot->since = now; slot->maxGap = 0; }
    else if (gap > slot->maxGap) slot->maxGap = gap;
    *gapOut = gap;
    slot->last = now;
    return slot;
}

static inline TBSource *tbSrcTouch(TBSources *s, uintptr_t k, uint64_t now) {
    uint64_t gap;
    TBSource *e = tbSrcTouchIn(s, k, now, &gap);
    tbSrcLearn(e, gap);
    return e;
}

// Whether a source has changed for long enough to be restless.
static inline bool tbSrcLongEnough(const TBSources *s, const TBSource *e, uint64_t now) {
    uint64_t need = e->maxGap <= TB_FRAME_GAP_NS ? s->framesRestlessNs : TB_RESTLESS_AFTER_NS;
    return now - e->since >= need;
}

// A layer that is not tagged changed: whether it was found restless and is to be tagged now
// (the probe does it, except for a scroll view, and then calls tbSrcNoteTagged).
static inline bool tbSrcTakeVerdict(TBSources *s, uintptr_t k) {
    TBSource *e = tbSrcFind(s, k);
    bool tag = e && e->restless;
    if (tag) e->restless = false;
    return tag;
}

// A tagged layer changed in this turn.
static inline void tbSrcNoteTagged(TBSources *s, uintptr_t k) {
    for (int i = 0; i < s->taggedCount; i++) if (s->tagged[i] == k) return;
    if (s->taggedCount < TB_TURN_MAX) s->tagged[s->taggedCount++] = k;
}

// The app was sent a touch event in this turn: ended, it held the end of a touch (ended or
// cancelled); delivered, the probe let it reach the app.
// Only an end counts (a control acts on touch-up), and only one the app got: a touch moving
// (a drag) is no reason to count a ticker's tick, and a withheld one did nothing.
static inline void tbSrcNoteTouch(TBSources *s, bool ended, bool delivered) {
    if (ended && delivered) s->touchEnded = true;
}

// A turn with no change: its touch is over.
static inline void tbSrcResetTurn(TBSources *s) {
    s->taggedCount = 0;
    s->touchEnded = false;
}

// Whether a source had been still before now: over 150 ms (a main thread stall longer than
// that, just before a touch's end, can pass: accepted), and over 3 times its own rhythm, once
// known (tbSrcEndTurn says which turns teach it). A source with none, its changes all in
// re-layouts (issue #309's counter: four changes in 560 ms, each with 28 to 49 other layers, as
// the screen turned), has the 150 ms bound alone.
static inline bool tbSrcStill(const TBSource *e, uint64_t now) {
    if (now <= e->last) return false;
    uint64_t gap = now - e->last;
    return gap > TB_WOKE_MIN_NS && gap > TB_WOKE_RHYTHMS * e->interval;
}

// Whether source key (in band, 0: none known) changed in this turn after being still, and its
// band too. A key never seen is not: in a turn of restless sources it can only be a new layer
// of a restless band (SwiftUI replaces a TimelineView's layers every few frames). A layer whose
// address is reused can inherit its entry; in the worst case a change is missed.
static inline bool tbSrcWoke(TBSources *s, uintptr_t key, uintptr_t band, uint64_t now) {
    TBSource *e = tbSrcFind(s, key);
    if (!e || !tbSrcStill(e, now)) return false;
    TBSource *b = band ? tbSrcFind(s, band) : NULL;
    return !b || tbSrcStill(b, now);
}

// The end of a turn: records its changed layers (keys, with their bands, 0 when not known;
// the tagged ones came through tbSrcNoteTagged) and says whether the turn is restless, every
// source in it restless. overflow: more layers changed than were kept; scrolling: one of them
// is a scroll view that is dragged, decelerating or bouncing back.
static inline bool tbSrcEndTurn(TBSources *s, int n, const uintptr_t *keys, const uintptr_t *bands,
                                bool overflow, bool scrolling, uint64_t now) {
    bool restless = (n > 0 || s->taggedCount > 0) && !overflow && !scrolling;
    // Issue #309: the end of a touch, and a source in its turn that had been still, is the
    // touch's change. Judged before this turn's changes are recorded.
    bool woke = false;
    if (restless && s->touchEnded) {
        for (int i = 0; i < s->taggedCount && !woke; i++) {
            TBSource *e = tbSrcFind(s, s->tagged[i]);
            woke = tbSrcWoke(s, s->tagged[i], e ? e->band : 0, now);
        }
        for (int i = 0; i < n && !woke; i++) {
            TBSource *e = tbSrcFind(s, keys[i]);
            woke = tbSrcWoke(s, keys[i], bands[i] ? bands[i] : (e ? e->band : 0), now);
        }
    }
    // Whether every source in the turn was known as restless before it: tagged, or a layer of a
    // band already restless at its previous change (a SwiftUI layer replaced in a restless band
    // is never tagged). Judged before the turn's changes are recorded, and not by now: #309's
    // last rotation turn makes its ~30 layers and their bands restless as it comes.
    bool knownRestless = !overflow && n + s->taggedCount > 0;
    for (int i = 0; i < n && knownRestless; i++) {
        TBSource *e = tbSrcFind(s, keys[i]);
        uintptr_t band = bands[i] ? bands[i] : (e ? e->band : 0);
        TBSource *b = band ? tbSrcFind(s, band) : NULL;
        knownRestless = b && tbSrcLongEnough(s, b, b->last);
    }
    // Each source's gap, for its rhythm once the turn is judged (below).
    struct { TBSource *e; uintptr_t k; uint64_t gap; } gaps[3 * TB_TURN_MAX];
    int ng = 0;
#define TB_GAP(slot_, k_, g_) do { gaps[ng].e = (slot_); gaps[ng].k = (k_); gaps[ng].gap = (g_); ng++; } while (0)
    uint64_t gap;
    for (int i = 0; i < s->taggedCount; i++) {
        TBSource *e = tbSrcTouchIn(s, s->tagged[i], now, &gap);
        TB_GAP(e, s->tagged[i], gap);
    }
    bool crowded = overflow || n + s->taggedCount >= TB_CROWDED_TURN;
    uintptr_t touched[TB_TURN_MAX];
    int nt = 0;
    for (int i = 0; i < n; i++) {
        TBSource *slot = tbSrcTouchIn(s, keys[i], now, &gap);
        TB_GAP(slot, keys[i], gap);
        if (bands[i]) slot->band = bands[i];
        uintptr_t band = slot->band; // a layer just removed keeps the band it was in
        bool layerRestless = tbSrcLongEnough(s, slot, now);
        if (layerRestless) slot->restless = true; // tagged on its next change (tbSrcTakeVerdict)
        bool bandRestless = false;
        if (band) {
            bool seen = false;
            for (int j = 0; j < nt; j++) if (touched[j] == band) { seen = true; break; }
            TBSource *b = seen ? tbSrcFind(s, band) : tbSrcTouchIn(s, band, now, &gap);
            if (!seen) { TB_GAP(b, band, gap); if (nt < TB_TURN_MAX) touched[nt++] = band; }
            bandRestless = b && tbSrcLongEnough(s, b, now);
        }
        if (!layerRestless && !bandRestless) restless = false;
    }
#undef TB_GAP
    // The rhythm: a source's own changes. Not from a touch's end (its change is the touch's: a
    // tap's gap would make the next tap's wait for a still source too long), nor from a crowded
    // turn of sources not known as restless (a rotation, a push: the screen laid out again, its
    // gaps the screen's). A crowded turn of known restless ones (above) teaches it: rows of
    // prices ticking together, with a sparkline SwiftUI replaces on every tick.
    if (!s->touchEnded && (!crowded || knownRestless)) {
        for (int i = 0; i < ng; i++)
            if (gaps[i].e && gaps[i].e->key == gaps[i].k) tbSrcLearn(gaps[i].e, gaps[i].gap);
    }
    tbSrcResetTurn(s);
    return restless && !woke;
}

#endif
