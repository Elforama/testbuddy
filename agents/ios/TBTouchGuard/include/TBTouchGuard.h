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

#endif
