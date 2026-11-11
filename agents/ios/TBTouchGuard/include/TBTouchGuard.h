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

// tbWithholdTouch: whether a touch going down at (px, py) must not reach the app. live is the
// target's accessibility frame now; armed is the frame the agent read just before it sent the
// touch (packed; 0: none).
static inline bool tbWithholdTouch(TBFrame live, double px, double py, uint64_t armed) {
    // Off the target (with a point of slack): it is not where the tap was aimed.
    if (!(px >= live.x - 1 && px < live.x + live.w + 1 && py >= live.y - 1 && py < live.y + live.h + 1)) return true;
    if (!armed) return false;
    // Moved since the agent read it (issue #103): a move that starts between that read and the
    // touch. The point can still be on its frame, but SwiftUI lays a moving view out where the
    // move ends, and its button does not take the touch. Withheld, the host finds the target
    // again once it is still. Its middle, not its edges: a button pulsing around its middle
    // changes size, not place, and stays tappable.
    TBFrame read = tbUnpackFrame(armed);
    return fabs((live.x + live.w / 2) - (read.x + read.w / 2)) > 0.5 || fabs((live.y + live.h / 2) - (read.y + read.h / 2)) > 0.5;
}

#endif
