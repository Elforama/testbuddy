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
// Motion that makes no commit is reported too:
//   * SwiftUI (iOS 26+) runs its animations (a custom transition, a spring) by updating
//     CAPresentationModifiers frame by frame from its own thread, with no commit at all. Each
//     update counts as a change (at most one notification per 30 ms), unless that layer's
//     property has been moving for 2.5 s (a repeatForever animation: SwiftUI replaces its
//     modifiers now and then, so the layer and key path are what is followed).
//   * Core Animation animations run in the render server once added. Each finite one added
//     (-addAnimation:forKey:) moves "busy until" to its end; after the commit the agent is told
//     how long the app stays busy ("<name>.busy", state pid<<32 | ms), and does not take the UI
//     for settled before then. Animations that never end (a spinner) or run longer than 3 s
//     do not count: they never let the screen settle.
//
// Layer hooks observe commits. A named simulator tap armed by tb can be withheld before
// delivery when its identified target moved. The probe also hides the keyboard on request: the agent posts
// "dev.testbuddy.probe.resign.<pid>" (hideKeyboard), and the first responder resigns, which no
// touch can do in a screen that does not dismiss its keyboard on a drag.
#import <Foundation/Foundation.h>
#import <QuartzCore/QuartzCore.h>
#import <UIKit/UIKit.h>
#import <objc/runtime.h>
#import <notify.h>
#import <stdatomic.h>
#import "TBFlutter.h"
#import <unistd.h>
#import <math.h>
#import <os/lock.h>
#import <mach/mach_time.h>

static atomic_bool tbDirty;
static int tbToken = -1;
static int tbBusyToken = -1;
static atomic_uint tbCount;
static const char *tbName = "dev.testbuddy.probe.changed";
static char tbBusyName[160];

// Restless sources. A source that has changed in every stretch of 400 ms for 500 ms (a ticker,
// a stopwatch, a progress counter) is restless. One that changes every frame (gaps up to
// 70 ms: an animation) only after 1 s, and a presentation modifier (SwiftUI's animations)
// after 2.5 s, so a finite animation is waited out (a SwiftUI transition can take 1.5 s),
// while one that never ends (a pulse, a marquee) stops holding the screen once it is known.
// With animations sped up (tb launches apps with -UIAnimationDragCoefficient 0.1, which
// SwiftUI follows too) both scale down, to no less than 500 ms and 1 s.
// When every source that changed in a run loop turn is restless, the notification carries the
// restless bit: the UI did change, but that change should not keep the screen from counting
// as settled.
//
// A source is a layer, or a band: the layers of one class at the same height in the same
// superlayer. SwiftUI replaces some layers every few frames (a TimelineView's progress bar,
// a ticker's text), so the layer alone never stays long enough to be found restless; its band
// does.
//
// A layer found restless is tagged (an associated object, gone with the layer) and stays
// restless: a ticker that paused while its screen was hidden is not learned again when the
// screen comes back. Scroll views are never tagged, and never restless while dragged,
// decelerating or bouncing back: a list scrolling for a while now is no animation, and a tap
// on a moving list only stops it.
#define TB_TURN_MAX 128
#define TB_TABLE 4096
#define TB_RESTLESS_AFTER_NS 500000000ull
#define TB_FRAMES_RESTLESS_AFTER_NS 1000000000ull
#define TB_MODIFIER_ENDLESS_AFTER_NS 2500000000ull
#define TB_MODIFIER_ENDLESS_MIN_NS 1000000000ull
#define TB_ENDLESS_RECENT_NS 2000000000ull
#define TB_FRAME_GAP_NS 70000000ull
#define TB_RESTLESS_GAP_NS 400000000ull
#define TB_MOTION_POST_NS 30000000ull
#define TB_BUSY_MAX_NS 3000000000ull
static os_unfair_lock tbLock = OS_UNFAIR_LOCK_INIT;
static uintptr_t tbTurn[TB_TURN_MAX];
// Weak references to this turn's layers, kept by hand (the runtime's OrNil variants: a layer
// marked while it is deallocating is simply gone at the commit, where ARC's would abort).
static void *tbTurnLayer[TB_TURN_MAX];
extern id objc_storeWeakOrNil(void **location, id obj);
extern void *objc_loadWeakRetained(void **location);
static int tbTurnCount;
static bool tbTurnOverflow;
static bool tbTurnTagged; // a tagged (restless) layer changed this turn
typedef struct { uintptr_t key, band; uint64_t since, last, maxGap; bool restless; } tbEntry;
static tbEntry tbTable[TB_TABLE];
static char tbRestlessTag;
static char tbModifierKey; // a presentation modifier's source: its layer and key path
static char tbEndlessTag;  // a layer whose presentation modifiers move forever
static atomic_ullong tbBusyUntil;   // ns (tbNowNs): the last finite animation's end
static uint64_t tbBusyReported;     // main thread
static atomic_ullong tbMotionPosted; // ns: the last notification for presentation modifiers

static uint64_t tbNowNs(void) {
    static mach_timebase_info_data_t tb;
    if (tb.denom == 0) mach_timebase_info(&tb);
    return mach_absolute_time() * tb.numer / tb.denom;
}

// The table slot for a key, or NULL. Caller holds tbLock.
static tbEntry *tbFind(uintptr_t k) {
    size_t h = (k >> 4) * 2654435761u;
    for (int j = 0; j < 8; j++) {
        tbEntry *e = &tbTable[(h + j) & (TB_TABLE - 1)];
        if (e->key == k) return e;
    }
    return NULL;
}

// Records a change of source k now: its slot (the least recently changed one in its probe
// window is reused). Caller holds tbLock.
static tbEntry *tbTouch(uintptr_t k, uint64_t now) {
    size_t h = (k >> 4) * 2654435761u;
    tbEntry *slot = NULL, *oldest = NULL;
    for (int j = 0; j < 8; j++) {
        tbEntry *e = &tbTable[(h + j) & (TB_TABLE - 1)];
        if (e->key == k) { slot = e; break; }
        if (!oldest || e->last < oldest->last) oldest = e;
    }
    if (!slot) { slot = oldest; *slot = (tbEntry){ .key = k, .since = now, .last = now }; }
    uint64_t gap = now - slot->last;
    if (gap > TB_RESTLESS_GAP_NS) { slot->since = now; slot->maxGap = 0; }
    else if (gap > slot->maxGap) slot->maxGap = gap;
    slot->last = now;
    return slot;
}

// How long an animation must run to be taken for endless: frame by frame on layers, and
// through presentation modifiers (see above).
static uint64_t tbFramesRestlessNs = TB_FRAMES_RESTLESS_AFTER_NS, tbModifierEndlessNs = TB_MODIFIER_ENDLESS_AFTER_NS;
static void tbReadSpeed(void) {
    double c = [NSUserDefaults.standardUserDefaults doubleForKey:@"UIAnimationDragCoefficient"];
    if (!(c > 0) || c >= 1) return;
    tbFramesRestlessNs = (uint64_t)fmax(TB_RESTLESS_AFTER_NS, TB_FRAMES_RESTLESS_AFTER_NS * c);
    tbModifierEndlessNs = (uint64_t)fmax(TB_MODIFIER_ENDLESS_MIN_NS, TB_MODIFIER_ENDLESS_AFTER_NS * c);
}

// Whether a layer or band has changed for long enough to be restless (see above).
static bool tbLongEnough(tbEntry *e, uint64_t now) {
    uint64_t need = e->maxGap <= TB_FRAME_GAP_NS ? tbFramesRestlessNs : TB_RESTLESS_AFTER_NS;
    return now - e->since >= need;
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
        if (!seen) {
            objc_storeWeakOrNil(&tbTurnLayer[tbTurnCount], layer);
            tbTurn[tbTurnCount++] = k;
        }
    } else {
        tbTurnOverflow = true; // many layers at once: a real change
    }
    os_unfair_lock_unlock(&tbLock);
}

// A layer's band key (odd, so it never equals a pointer), or 0 when it is not in a tree.
static uintptr_t tbBand(CALayer *l) {
    CALayer *sup = l.superlayer;
    if (!sup) return 0;
    CGRect f = l.frame;
    if (!isfinite(f.origin.y) || !isfinite(f.size.height)) return 0;
    id d = l.delegate;
    uintptr_t cls = (uintptr_t)(__bridge void *)(d ? object_getClass(d) : object_getClass(l));
    uintptr_t k = (uintptr_t)(__bridge void *)sup;
    k ^= cls * 0x9E3779B97F4A7C15ull;
    k ^= (uintptr_t)(intptr_t)lround(f.origin.y) * 0xC2B2AE3D27D4EB4Full;
    k ^= (uintptr_t)(intptr_t)lround(f.size.height) * 0x165667B19E3779F9ull;
    return k | 1;
}

// A scroll view that is dragged, decelerating or bouncing back.
static bool tbScrolling(CALayer *l) {
    id v = l.delegate;
    if (![v isKindOfClass:UIScrollView.class]) return false;
    UIScrollView *sv = v;
    return sv.isTracking || sv.isDragging || sv.isDecelerating;
}

// Records this turn's changed layers; true when every one of them is restless. Main thread
// (the run loop observer).
static bool tbTurnRestless(void) {
    uint64_t now = tbNowNs();
    os_unfair_lock_lock(&tbLock);
    int n = tbTurnCount;
    uintptr_t keys[TB_TURN_MAX];
    CALayer *layers[TB_TURN_MAX];
    for (int i = 0; i < n; i++) {
        keys[i] = tbTurn[i];
        layers[i] = (__bridge_transfer CALayer *)objc_loadWeakRetained(&tbTurnLayer[i]); // or nil if it is gone
        objc_storeWeakOrNil(&tbTurnLayer[i], nil);
    }
    bool restless = (n > 0 || tbTurnTagged) && !tbTurnOverflow;
    tbTurnCount = 0;
    tbTurnOverflow = false;
    tbTurnTagged = false;
    os_unfair_lock_unlock(&tbLock);

    // Layer properties are read outside the lock (they take Core Animation's).
    uintptr_t bands[TB_TURN_MAX];
    bool scrolling = false;
    for (int i = 0; i < n; i++) {
        bands[i] = layers[i] ? tbBand(layers[i]) : 0;
        if (layers[i] && tbScrolling(layers[i])) scrolling = true;
        layers[i] = nil;
    }
    if (scrolling) restless = false;

    os_unfair_lock_lock(&tbLock);
    uintptr_t touched[TB_TURN_MAX];
    int nt = 0;
    for (int i = 0; i < n; i++) {
        tbEntry *slot = tbTouch(keys[i], now);
        if (bands[i]) slot->band = bands[i];
        uintptr_t band = slot->band; // a layer just removed keeps the band it was in
        bool layerRestless = tbLongEnough(slot, now);
        if (layerRestless) slot->restless = true; // tagged on its next change (tbMark)
        bool bandRestless = false;
        if (band) {
            bool seen = false;
            for (int j = 0; j < nt; j++) if (touched[j] == band) { seen = true; break; }
            tbEntry *b = seen ? tbFind(band) : tbTouch(band, now);
            if (!seen) touched[nt++] = band;
            bandRestless = b && tbLongEnough(b, now);
        }
        if (!layerRestless && !bandRestless) restless = false;
    }
    os_unfair_lock_unlock(&tbLock);
    return restless;
}

static void tbPost(bool restless) {
    uint32_t c = (atomic_fetch_add_explicit(&tbCount, 1, memory_order_relaxed) + 1) & 0x7fffffff;
    if (c == 0) c = 1; // 0 means hello
    uint64_t low = c | (restless ? 0x80000000u : 0);
    notify_set_state(tbToken, ((uint64_t)(uint32_t)getpid() << 32) | low);
    notify_post(tbName);
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

// A Core Animation animation added to a layer: if it ends (and within 3 s), the app is busy
// until then. Times are in the layer's time space, scaled by every speed up the tree.
static void tbNoteAnimation(CALayer *layer, CAAnimation *anim) {
    if (![anim isKindOfClass:CAAnimation.class]) return;
    if (objc_getAssociatedObject(layer, &tbRestlessTag)) return;
    double speed = anim.speed;
    for (CALayer *l = layer; l && speed != 0; l = l.superlayer) speed *= l.speed;
    if (!(speed > 0) || !isfinite(speed)) return; // paused (driven by hand) or reversed
    double dur = anim.duration;
    if (dur <= 0) dur = 0.25; // Core Animation's default
    double total;
    float rc = anim.repeatCount;
    double rd = anim.repeatDuration;
    if (!isfinite(rc) || !isfinite(rd) || rc >= 1e6) return; // forever
    if (rd > 0) total = rd;
    else total = dur * (rc > 0 ? rc : 1) * (anim.autoreverses ? 2 : 1);
    double delay = 0;
    if (anim.beginTime > 0) delay = anim.beginTime - [layer convertTime:CACurrentMediaTime() fromLayer:nil];
    double secs = (delay + total) / speed;
    if (!isfinite(secs) || secs <= 0) return;
    if (secs * 1e9 > TB_BUSY_MAX_NS) return; // longer than any wait: decoration
    uint64_t end = tbNowNs() + (uint64_t)(secs * 1e9);
    uint64_t cur = atomic_load_explicit(&tbBusyUntil, memory_order_relaxed);
    while (end > cur && !atomic_compare_exchange_weak(&tbBusyUntil, &cur, end)) {}
}

static void (*addAnimation_orig)(id, SEL, id, NSString *);
static void addAnimation_hook(id self, SEL _cmd, id anim, NSString *key) {
    tbNoteAnimation(self, anim);
    tbMark(self);
    addAnimation_orig(self, _cmd, anim, key);
}

// A presentation modifier updated (SwiftUI's animations, frame by frame, often off the main
// thread): a change, unless its source has been moving long enough to be endless.
static void tbModifierMoved(id modifier) {
    if (objc_getAssociatedObject(modifier, &tbRestlessTag)) return;
    NSNumber *source = objc_getAssociatedObject(modifier, &tbModifierKey);
    uintptr_t k = source ? (uintptr_t)source.unsignedLongLongValue : (uintptr_t)(__bridge void *)modifier;
    uint64_t now = tbNowNs();
    os_unfair_lock_lock(&tbLock);
    tbEntry *e = tbTouch(k, now);
    bool endless = now - e->since >= tbModifierEndlessNs;
    os_unfair_lock_unlock(&tbLock);
    // (e stays valid: entries are never freed, and a reused slot only skews one decision.)
    if (endless) {
        os_unfair_lock_lock(&tbLock);
        e->restless = true; // its layer is tagged when it gets its next modifier
        os_unfair_lock_unlock(&tbLock);
        objc_setAssociatedObject(modifier, &tbRestlessTag, (__bridge id)kCFBooleanTrue, OBJC_ASSOCIATION_ASSIGN);
        return;
    }
    uint64_t last = atomic_load_explicit(&tbMotionPosted, memory_order_relaxed);
    if (now - last < TB_MOTION_POST_NS) return;
    if (!atomic_compare_exchange_strong(&tbMotionPosted, &last, now)) return;
    tbPost(false);
}

// A presentation modifier attached to a layer: remember its source (odd, like a band key).
// SwiftUI replaces the modifiers of an animation that never ends every few seconds: a layer
// whose source was found endless shortly before is tagged, and its modifiers from then on are
// endless at once.
static void tbNoteModifier(CALayer *layer, id modifier) {
    if (!modifier) return;
    if (objc_getAssociatedObject(layer, &tbEndlessTag)) {
        objc_setAssociatedObject(modifier, &tbRestlessTag, (__bridge id)kCFBooleanTrue, OBJC_ASSOCIATION_ASSIGN);
        return;
    }
    NSString *kp = [modifier valueForKey:@"keyPath"];
    uintptr_t k = ((uintptr_t)(__bridge void *)layer ^ ((uintptr_t)kp.hash * 0x9E3779B97F4A7C15ull)) | 1;
    objc_setAssociatedObject(modifier, &tbModifierKey, @(k), OBJC_ASSOCIATION_RETAIN_NONATOMIC);
    uint64_t now = tbNowNs();
    os_unfair_lock_lock(&tbLock);
    tbEntry *e = tbFind(k);
    bool endless = e && e->restless && now - e->last < TB_ENDLESS_RECENT_NS;
    os_unfair_lock_unlock(&tbLock);
    if (endless) {
        objc_setAssociatedObject(layer, &tbEndlessTag, (__bridge id)kCFBooleanTrue, OBJC_ASSOCIATION_ASSIGN);
        objc_setAssociatedObject(modifier, &tbRestlessTag, (__bridge id)kCFBooleanTrue, OBJC_ASSOCIATION_ASSIGN);
    }
}

static void (*addPresentationModifier_orig)(id, SEL, id);
static void addPresentationModifier_hook(id self, SEL _cmd, id m) { tbNoteModifier(self, m); addPresentationModifier_orig(self, _cmd, m); }
static void (*setPresentationModifiers_orig)(id, SEL, id);
static void setPresentationModifiers_hook(id self, SEL _cmd, id ms) {
    if ([ms isKindOfClass:NSArray.class]) for (id m in (NSArray *)ms) tbNoteModifier(self, m);
    setPresentationModifiers_orig(self, _cmd, ms);
}

static void (*pmSetValue_orig)(id, SEL, id);
static void pmSetValue_hook(id self, SEL _cmd, id v) { tbModifierMoved(self); pmSetValue_orig(self, _cmd, v); }
static void (*pmSetValueVelocity_orig)(id, SEL, id, id);
static void pmSetValueVelocity_hook(id self, SEL _cmd, id v, id vel) { tbModifierMoved(self); pmSetValueVelocity_orig(self, _cmd, v, vel); }

static void tbSwizzle(Class c, SEL sel, IMP hook, IMP *orig) {
    if (!c) return;
    Method m = class_getInstanceMethod(c, sel);
    if (!m) return;
    *orig = method_setImplementation(m, hook);
}

static void tbAfterCommit(CFRunLoopObserverRef o, CFRunLoopActivity a, void *info) {
    if (!atomic_exchange_explicit(&tbDirty, false, memory_order_relaxed)) return;
    // Busy first: the agent knows how long to wait before it hears of the change.
    uint64_t until = atomic_load_explicit(&tbBusyUntil, memory_order_relaxed);
    uint64_t now = tbNowNs();
    if (until > now && until != tbBusyReported && tbBusyToken >= 0) {
        tbBusyReported = until;
        uint64_t ms = (until - now + 999999) / 1000000;
        notify_set_state(tbBusyToken, ((uint64_t)(uint32_t)getpid() << 32) | ms);
        notify_post(tbBusyName);
    }
    tbPost(tbTurnRestless());
}

// Acknowledge only an actual touch-driven UIControl action for the armed identifier.
// Read the nonce before dispatch: an action may navigate or replace its sender.
static int tbControlRequest = -1, tbControlResult = -1, tbControlPoint = -1, tbControlRejected = -1, tbControlDeadline = -1;
static uint32_t tbIDHash(NSString *identifier) {
    const unsigned char *p = (const unsigned char *)identifier.UTF8String;
    uint32_t hash = 2166136261u;
    if (!p || !*p) return 0;
    for (; *p; ++p) hash = (hash ^ *p) * 16777619u;
    return hash;
}

// Resolve an accessibility frame in the app on the main thread. Some custom
// containers cannot expose it. Leave their inputs alone. A bounded walk and
// uniqueness check avoid interpreting ambiguous or incomplete trees.
static CGRect tbTargetFrame(UIWindow *window, uint32_t identifier, UIControl **control) {
    NSMutableArray *stack = [NSMutableArray arrayWithObject:window];
    NSHashTable *seen = [NSHashTable hashTableWithOptions:NSPointerFunctionsObjectPointerPersonality];
    CGRect result = CGRectNull;
    const NSUInteger limit = 1024;
    while (stack.count) {
        NSObject *node = stack.lastObject;
        [stack removeLastObject];
        if ([seen containsObject:node]) continue;
        if (seen.count >= limit) return CGRectNull;
        [seen addObject:node];
        if ([node respondsToSelector:@selector(accessibilityIdentifier)] &&
            tbIDHash([(id)node accessibilityIdentifier]) == identifier) {
            if (!CGRectIsNull(result)) return CGRectNull;
            result = node.accessibilityFrame;
            if ([node isKindOfClass:UIControl.class]) *control = (UIControl *)node;
        }
        if ([node isKindOfClass:UIView.class]) {
            UIView *view = (UIView *)node;
            if (view.hidden || view.alpha == 0) continue;
            [stack addObjectsFromArray:view.subviews];
        }
        NSArray *children = node.accessibilityElements;
        if (children.count) {
            if (children.count > limit) return CGRectNull;
            [stack addObjectsFromArray:children];
        } else {
            NSInteger count = node.accessibilityElementCount;
            if (count == NSNotFound || count < 0) continue;
            if ((NSUInteger)count > limit) return CGRectNull;
            for (NSInteger i = 0; i < count; ++i) {
                id child = [node accessibilityElementAtIndex:i];
                if (child) [stack addObject:child];
            }
        }
    }
    return result;
}

// Fence only the single touch armed by the test agent at this PID and point.
// Reject at .began, before calling UIKit, and consume the rest of that same
// touch. An already-forwarded begin can never produce a rejected receipt.
static __weak UITouch *tbRejectedTouch;
static uint64_t tbRejectedNonce, tbLastObservedNonce;
static void (*tbSendEventOrig)(UIApplication *, SEL, UIEvent *);

// Observe the control event itself, whether the app registered a selector or a
// UIAction. Only a control associated with this armed physical touch is eligible.
@interface TBControlWitness : NSObject
@property(nonatomic, weak) UIControl *control;
@property(nonatomic, weak) UITouch *touch;
@property(nonatomic) uint64_t nonce;
@end
@implementation TBControlWitness
- (void)watch:(UIControl *)control touch:(UITouch *)touch nonce:(uint64_t)nonce {
    UIControlEvents events = UIControlEventTouchUpInside | UIControlEventPrimaryActionTriggered;
    [self.control removeTarget:self action:@selector(fired:event:) forControlEvents:events];
    self.control = control; self.touch = touch; self.nonce = nonce;
    [control addTarget:self action:@selector(fired:event:) forControlEvents:events];
}
- (void)fired:(UIControl *)sender event:(UIEvent *)event {
    uint64_t current = 0;
    if (sender == self.control && self.touch && [event.allTouches containsObject:self.touch] &&
        notify_get_state(tbControlRequest, &current) == NOTIFY_STATUS_OK && current == self.nonce)
        notify_set_state(tbControlResult, current);
}
@end
static TBControlWitness *tbWitness;

static void tbSendEvent(UIApplication *app, SEL cmd, UIEvent *event) {
    NSSet<UITouch *> *touches = event.type == UIEventTypeTouches ? event.allTouches : nil;
    UITouch *touch = touches.count == 1 ? touches.anyObject : nil;
    if (tbRejectedTouch && [touches containsObject:tbRejectedTouch]) {
        if (touch == tbRejectedTouch) {
            if (touch.phase == UITouchPhaseEnded || touch.phase == UITouchPhaseCancelled) {
                // Certify non-delivery only after withholding the entire sequence.
                notify_set_state(tbControlRejected, tbRejectedNonce);
                tbRejectedTouch = nil;
            }
            return;
        }
        // A separate touch joined the event. Never swallow or claim rejection
        // of a multi-touch interaction.
        tbRejectedTouch = nil;
    }
    uint64_t token = 0, encoded = 0, deadline = 0;
    if (touch && touch.phase == UITouchPhaseBegan && touch.window &&
        notify_get_state(tbControlRequest, &token) == NOTIFY_STATUS_OK && token && token != tbLastObservedNonce &&
        notify_get_state(tbControlDeadline, &deadline) == NOTIFY_STATUS_OK && tbNowNs() <= deadline &&
        notify_get_state(tbControlPoint, &encoded) == NOTIFY_STATUS_OK) {
        CGPoint armed = CGPointMake((int32_t)(encoded >> 32), (int32_t)encoded);
        UIWindow *window = touch.window;
        CGPoint point = [window convertPoint:[touch locationInView:window] toCoordinateSpace:window.screen.coordinateSpace];
        if (fabs(armed.x-point.x) <= 1 && fabs(armed.y-point.y) <= 1) {
            tbLastObservedNonce = token;
            // Most UIKit controls identify themselves at the touch point. SwiftUI
            // may expose a virtual element inside a shared hosting view.
            for (UIView *v = touch.view; v; v = v.superview) {
                if (tbIDHash(v.accessibilityIdentifier) == (uint32_t)token) {
                    if ([v isKindOfClass:UIControl.class])
                        [tbWitness watch:(UIControl *)v touch:touch nonce:token];
                    tbSendEventOrig(app, cmd, event);
                    return;
                }
            }
            UIControl *control = nil;
            CGRect frame = CGRectNull;
            @try { frame = tbTargetFrame(window, (uint32_t)token, &control); }
            @catch (NSException *error) { /* A custom accessibility container is opaque. */ }
            if (!CGRectIsNull(frame) && !CGRectIsEmpty(frame) && isfinite(frame.origin.x) &&
                isfinite(frame.origin.y) && !CGRectContainsPoint(CGRectInset(frame,-1,-1), point)) {
                tbRejectedTouch = touch;
                tbRejectedNonce = token;
                return;
            }
            if (control && !CGRectIsNull(frame)) [tbWitness watch:control touch:touch nonce:token];
        }
    }
    tbSendEventOrig(app, cmd, event);
}

__attribute__((constructor)) static void tbProbeInit(void) {
    // Apps only: not the extensions and helpers a launch may also start.
    NSString *path = NSBundle.mainBundle.bundlePath;
    if (![path hasSuffix:@".app"]) return;
    const char *custom = getenv("TB_PROBE_NOTIFY");
    if (custom && *custom) tbName = strdup(custom);
    if (notify_register_check(tbName, &tbToken) != NOTIFY_STATUS_OK) return;
    snprintf(tbBusyName, sizeof tbBusyName, "%s.busy", tbName);
    if (notify_register_check(tbBusyName, &tbBusyToken) != NOTIFY_STATUS_OK) tbBusyToken = -1;
    char request[96], result[96], point[96], rejected[96], deadline[96];
    snprintf(request, sizeof request, "dev.testbuddy.control.request.%d", getpid());
    snprintf(result, sizeof result, "dev.testbuddy.control.result.%d", getpid());
    snprintf(point, sizeof point, "dev.testbuddy.control.point.%d", getpid());
    snprintf(rejected, sizeof rejected, "dev.testbuddy.control.rejected.%d", getpid());
    snprintf(deadline, sizeof deadline, "dev.testbuddy.control.deadline.%d", getpid());
    if (notify_register_check(request, &tbControlRequest) == NOTIFY_STATUS_OK &&
        notify_register_check(result, &tbControlResult) == NOTIFY_STATUS_OK &&
        notify_register_check(point, &tbControlPoint) == NOTIFY_STATUS_OK &&
        notify_register_check(rejected, &tbControlRejected) == NOTIFY_STATUS_OK &&
        notify_register_check(deadline, &tbControlDeadline) == NOTIFY_STATUS_OK) {
        tbSwizzle(UIApplication.class, @selector(sendEvent:), (IMP)tbSendEvent, (IMP *)&tbSendEventOrig);
        tbWitness = [TBControlWitness new];
    }
    tbInstallFlutter(tbMark);
    tbReadSpeed();
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
    S(addAnimation:forKey:, addAnimation);
#undef S
    // Private, present since iOS 17 (SwiftUI animates through it on iOS 26+); skipped if missing.
    Class pm = NSClassFromString(@"CAPresentationModifier");
    tbSwizzle(pm, NSSelectorFromString(@"setValue:"), (IMP)pmSetValue_hook, (IMP *)&pmSetValue_orig);
    tbSwizzle(pm, NSSelectorFromString(@"setValue:velocity:"), (IMP)pmSetValueVelocity_hook, (IMP *)&pmSetValueVelocity_orig);
    tbSwizzle(c, NSSelectorFromString(@"addPresentationModifier:"), (IMP)addPresentationModifier_hook, (IMP *)&addPresentationModifier_orig);
    tbSwizzle(c, NSSelectorFromString(@"setPresentationModifiers:"), (IMP)setPresentationModifiers_hook, (IMP *)&setPresentationModifiers_orig);
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
