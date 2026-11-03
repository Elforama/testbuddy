// TBProbe: loaded into an app tb launches on a simulator (simctl launch with
// SIMCTL_CHILD_DYLD_INSERT_LIBRARIES), it tells the agent when the app's layer tree changed,
// so "the UI changed" is pushed for every change, not only those UIKit's accessibility
// announces (screen changes, alerts) — a label's text, a color, a view shown or hidden.
//
// How: the CALayer entry points UIKit, SwiftUI and Core Animation clients go through when the
// tree changes (a property set, which asks the layer for its action; content or layout
// invalidated; a sublayer added or removed) set a flag. A run loop observer that runs after
// Core Animation's commit (BeforeWaiting, last order) posts one Darwin notification when the
// flag is set: at most one per run loop turn, nothing at all while the app is idle. Its state
// carries the pid and a counter. Nothing here polls, blocks or allocates per change.
//
// It never changes what the app does: every hook calls the original implementation. The one
// thing it does on request is hide the keyboard: the agent posts
// "dev.testbuddy.probe.resign.<pid>" (hideKeyboard), and the first responder resigns, which no
// touch can do in a screen that does not dismiss its keyboard on a drag.
#import <Foundation/Foundation.h>
#import <QuartzCore/QuartzCore.h>
#import <UIKit/UIKit.h>
#import <objc/runtime.h>
#import <notify.h>
#import <stdatomic.h>
#import <unistd.h>
#import <os/lock.h>
#import <mach/mach_time.h>

static atomic_bool tbDirty;
static int tbToken = -1;
static uint32_t tbCount;
static const char *tbName = "dev.testbuddy.probe.changed";

// Restless layers. A layer that has changed in every stretch of 400 ms for 500 ms (a ticker,
// a stopwatch, a progress counter) is restless. (Per-frame changes are not taken as restless
// any sooner: a scripted 300 ms slide-in changes every frame too, and must be waited out.) When every layer that changed in a run loop turn is restless, the notification
// carries the restless bit: the UI did change, but that change should not keep the screen
// from counting as settled.
// A layer found restless is tagged (an associated object, gone with the layer) and stays
// restless: a ticker that paused while its screen was hidden is not learned again when the
// screen comes back. Scroll views are never tagged: a list scrolling for a while now is no
// animation the next time it moves.
#define TB_TURN_MAX 128
#define TB_TABLE 4096
#define TB_RESTLESS_AFTER_NS 500000000ull
#define TB_RESTLESS_GAP_NS 400000000ull
static os_unfair_lock tbLock = OS_UNFAIR_LOCK_INIT;
static uintptr_t tbTurn[TB_TURN_MAX];
static int tbTurnCount;
static bool tbTurnOverflow;
static bool tbTurnTagged; // a tagged (restless) layer changed this turn
typedef struct { uintptr_t key; uint64_t since, last; bool restless; } tbEntry;
static tbEntry tbTable[TB_TABLE];
static char tbRestlessTag;

// The table slot for a layer key, or NULL. Caller holds tbLock.
static tbEntry *tbFind(uintptr_t k) {
    size_t h = (k >> 4) * 2654435761u;
    for (int j = 0; j < 8; j++) {
        tbEntry *e = &tbTable[(h + j) & (TB_TABLE - 1)];
        if (e->key == k) return e;
    }
    return NULL;
}

static inline void tbMark(id layer) {
    atomic_store_explicit(&tbDirty, true, memory_order_relaxed);
    if (objc_getAssociatedObject(layer, &tbRestlessTag)) {
        os_unfair_lock_lock(&tbLock);
        tbTurnTagged = true;
        os_unfair_lock_unlock(&tbLock);
        return;
    }
    uintptr_t k = (uintptr_t)(__bridge void *)layer;
    os_unfair_lock_lock(&tbLock);
    tbEntry *e = tbFind(k);
    bool tag = e && e->restless;
    if (tag) e->restless = false; // tagged from now on
    os_unfair_lock_unlock(&tbLock);
    if (tag) {
        id view = [(CALayer *)layer delegate];
        if (![view isKindOfClass:UIScrollView.class]) {
            objc_setAssociatedObject(layer, &tbRestlessTag, (__bridge id)kCFBooleanTrue, OBJC_ASSOCIATION_ASSIGN);
            os_unfair_lock_lock(&tbLock);
            tbTurnTagged = true;
            os_unfair_lock_unlock(&tbLock);
            return;
        }
    }
    os_unfair_lock_lock(&tbLock);
    if (tbTurnCount < TB_TURN_MAX) {
        bool seen = false;
        for (int i = 0; i < tbTurnCount; i++) if (tbTurn[i] == k) { seen = true; break; }
        if (!seen) tbTurn[tbTurnCount++] = k;
    } else {
        tbTurnOverflow = true; // many layers at once: a real change
    }
    os_unfair_lock_unlock(&tbLock);
}

static uint64_t tbNowNs(void) {
    static mach_timebase_info_data_t tb;
    if (tb.denom == 0) mach_timebase_info(&tb);
    return mach_absolute_time() * tb.numer / tb.denom;
}

// Records this turn's changed layers; true when every one of them is restless.
static bool tbTurnRestless(void) {
    uint64_t now = tbNowNs();
    os_unfair_lock_lock(&tbLock);
    int n = tbTurnCount;
    bool restless = (n > 0 || tbTurnTagged) && !tbTurnOverflow;
    for (int i = 0; i < n; i++) {
        uintptr_t k = tbTurn[i];
        size_t h = (k >> 4) * 2654435761u;
        tbEntry *slot = NULL, *oldest = NULL;
        for (int j = 0; j < 8; j++) {
            tbEntry *e = &tbTable[(h + j) & (TB_TABLE - 1)];
            if (e->key == k) { slot = e; break; }
            if (!oldest || e->last < oldest->last) oldest = e;
        }
        if (!slot) { slot = oldest; *slot = (tbEntry){ .key = k, .since = now }; }
        if (now - slot->last > TB_RESTLESS_GAP_NS) slot->since = now;
        slot->last = now;
        if (now - slot->since >= TB_RESTLESS_AFTER_NS) {
            slot->restless = true; // tagged on its next change (tbMark)
        } else {
            restless = false;
        }
    }
    tbTurnCount = 0;
    tbTurnOverflow = false;
    tbTurnTagged = false;
    os_unfair_lock_unlock(&tbLock);
    return restless;
}

#define HOOK0(sel_, name_) \
    static void (*name_##_orig)(id, SEL); \
    static void name_##_hook(id self, SEL _cmd) { tbMark(self); name_##_orig(self, _cmd); }
#define HOOK1(sel_, name_, T) \
    static void (*name_##_orig)(id, SEL, T); \
    static void name_##_hook(id self, SEL _cmd, T a) { tbMark(self); name_##_orig(self, _cmd, a); }
#define HOOK2(sel_, name_, T1, T2) \
    static void (*name_##_orig)(id, SEL, T1, T2); \
    static void name_##_hook(id self, SEL _cmd, T1 a, T2 b) { tbMark(self); name_##_orig(self, _cmd, a, b); }

static id (*actionForKey_orig)(id, SEL, NSString *);
static id actionForKey_hook(id self, SEL _cmd, NSString *key) { tbMark(self); return actionForKey_orig(self, _cmd, key); }

HOOK0(setNeedsDisplay, setNeedsDisplay)
HOOK1(setNeedsDisplayInRect:, setNeedsDisplayInRect, CGRect)
HOOK0(setNeedsLayout, setNeedsLayout)
HOOK1(setContents:, setContents, id)
HOOK1(setHidden:, setHidden, BOOL)
HOOK1(setOpacity:, setOpacity, float)
HOOK1(setBackgroundColor:, setBackgroundColor, CGColorRef)
HOOK1(setPosition:, setPosition, CGPoint)
HOOK1(setBounds:, setBounds, CGRect)
HOOK1(addSublayer:, addSublayer, id)
HOOK2(insertSublayer:atIndex:, insertSublayerAtIndex, id, unsigned)
HOOK2(insertSublayer:below:, insertSublayerBelow, id, id)
HOOK2(insertSublayer:above:, insertSublayerAbove, id, id)
HOOK2(replaceSublayer:with:, replaceSublayer, id, id)
HOOK0(removeFromSuperlayer, removeFromSuperlayer)
HOOK1(setSublayers:, setSublayers, id)

static void tbSwizzle(Class c, SEL sel, IMP hook, IMP *orig) {
    Method m = class_getInstanceMethod(c, sel);
    if (!m) return;
    *orig = method_setImplementation(m, hook);
}

static void tbAfterCommit(CFRunLoopObserverRef o, CFRunLoopActivity a, void *info) {
    if (!atomic_exchange_explicit(&tbDirty, false, memory_order_relaxed)) return;
    bool restless = tbTurnRestless();
    tbCount = (tbCount + 1) & 0x7fffffff;
    if (tbCount == 0) tbCount = 1; // 0 means hello
    uint64_t low = tbCount | (restless ? 0x80000000u : 0);
    notify_set_state(tbToken, ((uint64_t)(uint32_t)getpid() << 32) | low);
    notify_post(tbName);
}

__attribute__((constructor)) static void tbProbeInit(void) {
    // Apps only: not the extensions and helpers a launch may also start.
    NSString *path = NSBundle.mainBundle.bundlePath;
    if (![path hasSuffix:@".app"]) return;
    const char *custom = getenv("TB_PROBE_NOTIFY");
    if (custom && *custom) tbName = strdup(custom);
    if (notify_register_check(tbName, &tbToken) != NOTIFY_STATUS_OK) return;
    Class c = [CALayer class];
#define S(sel_, name_) tbSwizzle(c, @selector(sel_), (IMP)name_##_hook, (IMP *)&name_##_orig)
    S(actionForKey:, actionForKey);
    S(setNeedsDisplay, setNeedsDisplay);
    S(setNeedsDisplayInRect:, setNeedsDisplayInRect);
    S(setNeedsLayout, setNeedsLayout);
    S(setContents:, setContents);
    S(setHidden:, setHidden);
    S(setOpacity:, setOpacity);
    S(setBackgroundColor:, setBackgroundColor);
    S(setPosition:, setPosition);
    S(setBounds:, setBounds);
    S(addSublayer:, addSublayer);
    S(insertSublayer:atIndex:, insertSublayerAtIndex);
    S(insertSublayer:below:, insertSublayerBelow);
    S(insertSublayer:above:, insertSublayerAbove);
    S(replaceSublayer:with:, replaceSublayer);
    S(removeFromSuperlayer, removeFromSuperlayer);
    S(setSublayers:, setSublayers);
#undef S
    // After Core Animation's commit observer (order 2000000): the change is on its way to the screen.
    CFRunLoopObserverRef obs = CFRunLoopObserverCreate(NULL, kCFRunLoopBeforeWaiting | kCFRunLoopExit, true, INT_MAX, tbAfterCommit, NULL);
    CFRunLoopAddObserver(CFRunLoopGetMain(), obs, kCFRunLoopCommonModes);
    // hideKeyboard, on request.
    char resign[96];
    snprintf(resign, sizeof resign, "dev.testbuddy.probe.resign.%d", getpid());
    int resignToken;
    notify_register_dispatch(resign, &resignToken, dispatch_get_main_queue(), ^(int t) {
        [[UIApplication sharedApplication] sendAction:@selector(resignFirstResponder) to:nil from:nil forEvent:nil];
    });
    // Say hello: the agent learns this pid is probed.
    notify_set_state(tbToken, ((uint64_t)(uint32_t)getpid() << 32));
    notify_post(tbName);
}
