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

#endif
