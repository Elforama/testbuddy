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
#import "../TBTouchGuard/include/TBTouchGuard.h"
#import <unistd.h>
#import <fcntl.h>
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
// The sources' bookkeeping, and its constants, are in TBTouchGuard.h (TBSources).
#define TB_TABLE 4096
#define TB_MODIFIER_ENDLESS_AFTER_NS 2500000000ull
#define TB_MODIFIER_ENDLESS_MIN_NS 1000000000ull
#define TB_ENDLESS_RECENT_NS 2000000000ull
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
static TBSource tbTable[TB_TABLE];
static uintptr_t tbTaggedKeys[TB_TURN_MAX];
static TBSources tbSrc = {tbTable, TB_TABLE, TB_FRAMES_RESTLESS_AFTER_NS, tbTaggedKeys, 0, false};
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

// The table slot for a key, or NULL; a change of source k now (TBTouchGuard.h). Caller holds tbLock.
static TBSource *tbFind(uintptr_t k) { return tbSrcFind(&tbSrc, k); }
static TBSource *tbTouch(uintptr_t k, uint64_t now) { return tbSrcTouch(&tbSrc, k, now); }

// How long an animation must run to be taken for endless: frame by frame on layers, and
// through presentation modifiers (see above).
static uint64_t tbModifierEndlessNs = TB_MODIFIER_ENDLESS_AFTER_NS;
static void tbReadSpeed(void) {
    double c = [NSUserDefaults.standardUserDefaults doubleForKey:@"UIAnimationDragCoefficient"];
    if (!(c > 0) || c >= 1) return;
    tbSrc.framesRestlessNs = (uint64_t)fmax(TB_RESTLESS_AFTER_NS, TB_FRAMES_RESTLESS_AFTER_NS * c);
    tbModifierEndlessNs = (uint64_t)fmax(TB_MODIFIER_ENDLESS_MIN_NS, TB_MODIFIER_ENDLESS_AFTER_NS * c);
}

static inline void tbMark(id layer) {
    atomic_store_explicit(&tbDirty, true, memory_order_relaxed);
    uintptr_t k = (uintptr_t)(__bridge void *)layer;
    if (objc_getAssociatedObject(layer, &tbRestlessTag)) {
        os_unfair_lock_lock(&tbLock);
        tbSrcNoteTagged(&tbSrc, k);
        os_unfair_lock_unlock(&tbLock);
        return;
    }
    os_unfair_lock_lock(&tbLock);
    bool tag = tbSrcTakeVerdict(&tbSrc, k); // tagged from now on
    os_unfair_lock_unlock(&tbLock);
    if (tag) {
        id view = [(CALayer *)layer delegate];
        if (![view isKindOfClass:UIScrollView.class]) {
            objc_setAssociatedObject(layer, &tbRestlessTag, (__bridge id)kCFBooleanTrue, OBJC_ASSOCIATION_ASSIGN);
            os_unfair_lock_lock(&tbLock);
            tbSrcNoteTagged(&tbSrc, k);
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
    // The commit observer can still hold a detached layer after its UIKit view
    // is gone. Such a layer cannot represent active scrolling.
    if (!l.superlayer) return false;
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
    bool overflow = tbTurnOverflow;
    tbTurnCount = 0;
    tbTurnOverflow = false;
    os_unfair_lock_unlock(&tbLock);

    // Layer properties are read outside the lock (they take Core Animation's).
    uintptr_t bands[TB_TURN_MAX];
    bool scrolling = false;
    for (int i = 0; i < n; i++) {
        bands[i] = layers[i] ? tbBand(layers[i]) : 0;
        if (layers[i] && tbScrolling(layers[i])) scrolling = true;
        layers[i] = nil;
    }

    os_unfair_lock_lock(&tbLock);
    bool restless = tbSrcEndTurn(&tbSrc, n, keys, bands, overflow, scrolling, now);
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
    TBSource *e = tbTouch(k, now);
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
    TBSource *e = tbFind(k);
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
    if (!atomic_exchange_explicit(&tbDirty, false, memory_order_relaxed)) {
        // A turn with no change: its touch is over (TBTouchGuard.h tbSrcEndTurn).
        os_unfair_lock_lock(&tbLock);
        tbSrcResetTurn(&tbSrc);
        os_unfair_lock_unlock(&tbLock);
        return;
    }
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
static int tbControlFrame = -1; // the target's frame as the agent read it before the touch (TBTouchGuard.h)
static int tbControlCompleted = -1;
static char tbControlCompletedName[96];
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
static __weak UITouch *tbObservedTouch;
static uint64_t tbObservedNonce, tbLastObservedNonce;
static bool tbWithholdingTouch, tbTargetedTouch;
static bool tbWithheldMoved; // withheld because the target moved since the agent read it
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

// Issue #324: whether touch went to the view with the target's bounds, or one inside it (its
// hit-test view or a superview of it is that view). Bounded: a deep tree tells nothing.
static bool tbTouchOnView(UITouch *touch, TBFrame target) {
    UIWindow *window = touch.window;
    UIScreen *screen = window.screen;
    if (!window || !screen) return false;
    CGSize size = screen.bounds.size;
    // The chain up from the hit-test view, for tbTouchReachedTarget (TBTouchGuard.h): the
    // button itself (an accessibility element with its bounds), reached through views inside it,
    // none concealed. A deeper chain tells nothing: the walk stops there.
    TBChainView chain[64];
    int n = 0;
    for (UIView *v = touch.view; v && v != window && n < 64; v = v.superview, ++n) {
        CGRect f = [v convertRect:v.bounds toCoordinateSpace:screen.coordinateSpace];
        chain[n].frame = (TBFrame){f.origin.x, f.origin.y, f.size.width, f.size.height};
        chain[n].element = v.isAccessibilityElement;
        chain[n].concealed = v.hidden || v.alpha < 0.01 || v.accessibilityElementsHidden;
    }
    return tbTouchReachedTarget(chain, n, target, size.width, size.height);
}

// Sends an event on to the app, unless a guarded touch is withheld: whether it was sent.
static bool tbDeliverEvent(UIApplication *app, SEL cmd, UIEvent *event) {
    NSSet<UITouch *> *touches = event.type == UIEventTypeTouches ? event.allTouches : nil;
    UITouch *touch = touches.count == 1 ? touches.anyObject : nil;
    if (tbObservedTouch && [touches containsObject:tbObservedTouch]) {
        if (touch == tbObservedTouch) {
            bool withheld = tbWithholdingTouch;
            bool moved = tbWithheldMoved;
            bool targeted = tbTargetedTouch;
            bool finished = touch.phase == UITouchPhaseEnded || touch.phase == UITouchPhaseCancelled;
            uint64_t nonce = tbObservedNonce;
            if (finished) tbObservedTouch = nil;
            if (!withheld) tbSendEventOrig(app, cmd, event);
            if (finished) {
                // Publish the verdict before waking the agent. XCTest's input callback
                // can come before UIKit dispatches the end of this particular touch.
                if (withheld) notify_set_state(tbControlRejected, tbRejection(nonce, moved));
                notify_set_state(tbControlCompleted, tbTouchReceipt(nonce, targeted, touch.phase == UITouchPhaseCancelled));
                notify_post(tbControlCompletedName);
            }
            return !withheld;
        }
        // A separate touch joined the event. Never swallow or claim rejection
        // of a multi-touch interaction.
        tbObservedTouch = nil;
    }
    uint64_t token = 0, encoded = 0, deadline = 0, armedFrame = 0;
    if (touch && touch.phase == UITouchPhaseBegan && touch.window &&
        notify_get_state(tbControlRequest, &token) == NOTIFY_STATUS_OK && token && token != tbLastObservedNonce &&
        notify_get_state(tbControlDeadline, &deadline) == NOTIFY_STATUS_OK && tbNowNs() <= deadline &&
        notify_get_state(tbControlPoint, &encoded) == NOTIFY_STATUS_OK) {
        CGPoint armed = CGPointMake((int32_t)(encoded >> 32), (int32_t)encoded);
        UIWindow *window = touch.window;
        CGPoint point = [window convertPoint:[touch locationInView:window] toCoordinateSpace:window.screen.coordinateSpace];
        if (fabs(armed.x-point.x) <= 1 && fabs(armed.y-point.y) <= 1) {
            tbLastObservedNonce = token;
            tbObservedTouch = touch;
            tbObservedNonce = token;
            tbWithholdingTouch = false;
            tbWithheldMoved = false;
            tbTargetedTouch = false;
            // Issue #324: a watched touch (a button tb found by its text) is never withheld.
            // Its receipt says whether it went to the view with the bounds tb read, or one inside
            // it (TBTouchGuard.h tbViewIsTarget), rather than something over it.
            if ((uint32_t)token == TBWatchedTouch) {
                if (tbControlFrame < 0 || notify_get_state(tbControlFrame, &armedFrame) != NOTIFY_STATUS_OK) armedFrame = 0;
                tbTargetedTouch = armedFrame && tbTouchOnView(touch, tbUnpackFrame(armedFrame));
                tbSendEventOrig(app, cmd, event);
                return true;
            }
            // Most UIKit controls identify themselves at the touch point. SwiftUI
            // may expose a virtual element inside a shared hosting view.
            for (UIView *v = touch.view; v; v = v.superview) {
                if (tbIDHash(v.accessibilityIdentifier) == (uint32_t)token) {
                    tbTargetedTouch = true;
                    if ([v isKindOfClass:UIControl.class])
                        [tbWitness watch:(UIControl *)v touch:touch nonce:token];
                    tbSendEventOrig(app, cmd, event);
                    return true;
                }
            }
            UIControl *control = nil;
            CGRect frame = CGRectNull;
            @try { frame = tbTargetFrame(window, (uint32_t)token, &control); }
            @catch (NSException *error) { /* A custom accessibility container is opaque. */ }
            // Off the point, or moved since the agent read it (TBTouchGuard.h): withheld.
            if (tbControlFrame < 0 || notify_get_state(tbControlFrame, &armedFrame) != NOTIFY_STATUS_OK) armedFrame = 0;
            TBFrame live = {frame.origin.x, frame.origin.y, frame.size.width, frame.size.height};
            TBWithheld why = TBWithheldNone;
            if (!CGRectIsNull(frame) && !CGRectIsEmpty(frame) && isfinite(frame.origin.x) && isfinite(frame.origin.y))
                why = tbWithholdReason(live, point.x, point.y, armedFrame);
            if (why != TBWithheldNone) {
                tbWithholdingTouch = true;
                tbWithheldMoved = why == TBWithheldMoved; // the rejection says which (tbRejection)
                return false;
            }
            tbTargetedTouch = !CGRectIsNull(frame) && CGRectContainsPoint(frame, point);
            if (control && !CGRectIsNull(frame)) [tbWitness watch:control touch:touch nonce:token];
        }
    }
    tbSendEventOrig(app, cmd, event);
    return true;
}

// The gesture trace (issue #337; TBGestureTrace.h): what the app made of a gesture, for one that
// reached the app and had no effect. Only with TB_GESTURE_TRACE set (a file): otherwise nothing
// below is installed, and tbSendEvent pays one test of a bool. It changes no input: it reads
// state (scroll views, recognizers, touches) around what UIKit does anyway, adds two run loop
// observers that only read the time and scroll views' state, and writes each gesture from a
// background queue 1.5 s after its last lift (later while a scroll view it touched still moves,
// at most 5 s), so it never wakes the main thread.
static bool tbTraceOn;
static char *tbTracePath;
static dispatch_queue_t tbTraceQueue;
static os_unfair_lock tbTraceLock = OS_UNFAIR_LOCK_INIT; // the ring and the scalars below
static TBTraceRing tbTraceRing;
static bool tbTraceActive;
static int tbTraceGesture;
static uint64_t tbTraceStart, tbTraceLastLift, tbTraceGen;
static double tbTraceWall;
static int tbTraceAwaitingSettle; // scroll views released and still moving
// Main thread only.
static int tbTraceDown;
static int tbTraceLifts; // the touches the event being delivered lifts
static uint64_t tbTraceLastMove, tbTraceTurnStart, tbTraceLastDown;
static UIEvent *tbTraceEventNow; // the event sendEvent is delivering
static NSMapTable *tbTraceTouches, *tbTraceScrolls, *tbTraceRecognizers; // weak keys
static int tbTraceNextScroll, tbTraceNextRecognizer, tbTraceNextTouch;
static Ivar tbTracePageIvar, tbTraceHVelIvar, tbTraceVVelIvar;
static void (*tbTraceSetStateOrig)(id, SEL, UIGestureRecognizerState);

@interface TBTraceScrollInfo : NSObject
@property(nonatomic) int ident;
@property(nonatomic) bool awaitingSettle;
@property(nonatomic) CGPoint liftOffset;
@property(nonatomic) bool draggedAtLift;
@end
@implementation TBTraceScrollInfo
@end
@interface TBTraceRecognizerInfo : NSObject
@property(nonatomic) int ident;
@property(nonatomic) bool onTouch;
@property(nonatomic) uint64_t recognizedAt; // ns, when it last began or ended
@end
@implementation TBTraceRecognizerInfo
@end

static bool tbTraceIsActive(void) {
    os_unfair_lock_lock(&tbTraceLock);
    bool active = tbTraceActive;
    os_unfair_lock_unlock(&tbTraceLock);
    return active;
}

static void tbTraceAdd(TBTraceEntry *x) {
    os_unfair_lock_lock(&tbTraceLock);
    if (tbTraceActive) tbTracePush(&tbTraceRing, x);
    os_unfair_lock_unlock(&tbTraceLock);
}

static void tbTraceName(char *dst, size_t len, id obj) {
    tbTraceCopy(dst, len, obj ? class_getName(object_getClass(obj)) : "nil");
}

// A private ivar of UIScrollView, if it is there with the type expected (else NULL: not traced).
static Ivar tbTraceIvar(const char *name, const char *type) {
    Ivar v = class_getInstanceVariable(UIScrollView.class, name);
    const char *t = v ? ivar_getTypeEncoding(v) : NULL;
    return t && strncmp(t, type, strlen(type)) == 0 ? v : NULL;
}
static double tbTraceIvarDouble(id obj, Ivar v) {
    if (!v) return NAN;
    double d;
    memcpy(&d, (char *)(__bridge void *)obj + ivar_getOffset(v), sizeof d);
    return d;
}

// Ends the gesture: formats it, and appends it to the file with one write (O_APPEND: whole
// blocks, from any process) off the main thread. Caller holds tbTraceLock.
static void tbTraceEndLocked(void) {
    if (!tbTraceActive) return;
    tbTraceActive = false;
    tbTraceGen++;
    size_t len = TB_TRACE_LINE * (tbTraceRing.count + 2);
    char *buf = malloc(len);
    if (!buf) { tbTraceReset(&tbTraceRing); return; }
    size_t n = tbTraceFormatGesture(tbTraceGesture, getpid(), NSBundle.mainBundle.bundleIdentifier.UTF8String,
                                    tbTraceWall, tbTraceStart, &tbTraceRing, buf, len);
    tbTraceReset(&tbTraceRing);
    dispatch_async(tbTraceQueue, ^{
        int fd = open(tbTracePath, O_WRONLY | O_APPEND | O_CREAT, 0644);
        if (fd >= 0) { (void)write(fd, buf, n); close(fd); }
        free(buf);
    });
}

// The gesture ends this long from now, unless a touch comes first (a new generation).
static void tbTraceScheduleEnd(uint64_t gen, uint64_t delayNs) {
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)delayNs), tbTraceQueue, ^{
        os_unfair_lock_lock(&tbTraceLock);
        if (gen == tbTraceGen && tbTraceActive) {
            if (tbTraceAwaitingSettle > 0 && tbNowNs() - tbTraceLastLift < TB_TRACE_TAIL_MAX_NS)
                tbTraceScheduleEnd(gen, 500000000ull);
            else
                tbTraceEndLocked();
        }
        os_unfair_lock_unlock(&tbTraceLock);
    });
}

static void tbTraceScrollEntry(UIScrollView *sv, TBTraceMoment moment) {
    TBTraceScrollInfo *info = [tbTraceScrolls objectForKey:sv];
    if (!info) {
        info = [TBTraceScrollInfo new];
        info.ident = ++tbTraceNextScroll;
        info.liftOffset = CGPointMake(NAN, NAN);
        [tbTraceScrolls setObject:info forKey:sv];
    }
    CGPoint o = sv.contentOffset;
    // A release of a scroll view that was not dragged, does not move and does not page (a tap
    // on a list) tells nothing more than its lift.
    if (moment == TBMomentRelease && !info.draggedAtLift && !sv.isDecelerating && !sv.isPagingEnabled) return;
    TBTraceEntry x = {.t = tbNowNs(), .kind = TBTraceScroll, .a = moment, .id = info.ident};
    tbTraceName(x.name, sizeof x.name, sv);
    x.v[0] = o.x; x.v[1] = o.y;
    UIPanGestureRecognizer *pan = sv.panGestureRecognizer;
    if (moment == TBMomentDown || moment == TBMomentLift) { // the finger's
        CGPoint v = [pan velocityInView:sv];
        x.v[2] = v.x; x.v[3] = v.y;
    } else { // the content's own, once the finger is off
        x.v[2] = tbTraceIvarDouble(sv, tbTraceHVelIvar);
        x.v[3] = tbTraceIvarDouble(sv, tbTraceVVelIvar);
    }
    UIGestureRecognizerState ps = pan.state;
    x.flags = (sv.isTracking ? TBScrollTracking : 0) | (sv.isDragging ? TBScrollDragging : 0) |
              (sv.isDecelerating ? TBScrollDecelerating : 0) | (sv.isPagingEnabled ? TBScrollPaging : 0) |
              (ps == UIGestureRecognizerStateBegan || ps == UIGestureRecognizerStateChanged ? TBScrollPanBegan : 0) |
              (ps == UIGestureRecognizerStateFailed ? TBScrollPanFailed : 0);
    tbTraceAdd(&x);
    if (moment == TBMomentLift) { info.liftOffset = o; info.draggedAtLift = sv.isDragging; }
    if (moment != TBMomentRelease) return;
    if (sv.isPagingEnabled) {
        TBTraceEntry pt = {.t = x.t, .kind = TBTracePageTarget, .id = info.ident};
        memcpy(pt.name, x.name, sizeof pt.name);
        pt.v[0] = pt.v[1] = NAN;
        if (!sv.isDecelerating) { // not moving: it stays where it is
            pt.v[0] = o.x; pt.v[1] = o.y;
        } else if (tbTracePageIvar) {
            CGPoint target;
            memcpy(&target, (char *)(__bridge void *)sv + ivar_getOffset(tbTracePageIvar), sizeof target);
            pt.v[0] = target.x; pt.v[1] = target.y;
        }
        pt.v[2] = info.liftOffset.x; pt.v[3] = info.liftOffset.y;
        tbTraceAdd(&pt);
    }
    if (sv.isDecelerating && !info.awaitingSettle) { // "settled" when it stops
        info.awaitingSettle = true;
        os_unfair_lock_lock(&tbTraceLock);
        tbTraceAwaitingSettle++;
        os_unfair_lock_unlock(&tbTraceLock);
    }
}

// The scroll views a touch is in, innermost first (at most 8), and at a lift or release every
// one this gesture's touches went down in too (a touch taken by a pan can lose its view).
static void tbTraceScrollsUnder(UITouch *t, TBTraceMoment moment) {
    NSMutableArray<UIScrollView *> *under = [NSMutableArray array];
    for (UIView *v = t.view; v && under.count < 8; v = v.superview)
        if ([v isKindOfClass:UIScrollView.class]) [under addObject:(UIScrollView *)v];
    if (moment != TBMomentDown)
        for (UIScrollView *sv in [tbTraceScrolls keyEnumerator].allObjects)
            if (![under containsObject:sv]) [under addObject:sv];
    for (UIScrollView *sv in under) tbTraceScrollEntry(sv, moment);
}

static void tbTraceBegin(uint64_t now) {
    os_unfair_lock_lock(&tbTraceLock);
    tbTraceEndLocked(); // the one before, if its end has not come yet
    tbTraceActive = true;
    tbTraceGesture++;
    tbTraceStart = now;
    tbTraceWall = NSDate.date.timeIntervalSince1970;
    tbTraceAwaitingSettle = 0;
    os_unfair_lock_unlock(&tbTraceLock);
    [tbTraceTouches removeAllObjects];
    [tbTraceScrolls removeAllObjects];
    [tbTraceRecognizers removeAllObjects];
    tbTraceNextScroll = tbTraceNextRecognizer = tbTraceNextTouch = 0;
    tbTraceDown = 0;
    tbTraceLastMove = 0;
}

static TBTraceRecognizerInfo *tbTraceRecognizerInfo(UIGestureRecognizer *gr) {
    TBTraceRecognizerInfo *info = [tbTraceRecognizers objectForKey:gr];
    if (!info) {
        info = [TBTraceRecognizerInfo new];
        info.ident = ++tbTraceNextRecognizer;
        [tbTraceRecognizers setObject:info forKey:gr];
    }
    return info;
}

// A recognizer that sees a touch, the first time in this gesture.
static void tbTraceSees(UIGestureRecognizer *gr, uint64_t now) {
    TBTraceRecognizerInfo *info = tbTraceRecognizerInfo(gr);
    if (info.onTouch) return;
    info.onTouch = true;
    TBTraceEntry x = {.t = now, .kind = TBTraceSees, .id = info.ident};
    tbTraceName(x.name, sizeof x.name, gr);
    char what[48] = "", view[48];
    if ([gr isKindOfClass:UITapGestureRecognizer.class])
        snprintf(what, sizeof what, "taps %lu, ", (unsigned long)((UITapGestureRecognizer *)gr).numberOfTapsRequired);
    else if ([gr isKindOfClass:UILongPressGestureRecognizer.class])
        snprintf(what, sizeof what, "min %.2f s, ", ((UILongPressGestureRecognizer *)gr).minimumPressDuration);
    tbTraceName(view, sizeof view, gr.view);
    char name[64] = "";
    if (gr.name.length) snprintf(name, sizeof name, "\"%.40s\", ", gr.name.UTF8String);
    snprintf(x.extra, sizeof x.extra, "(%s%son %s, %s)", what, name, view, tbTraceStateName((int)gr.state));
    tbTraceAdd(&x);
}

// sendEvent, before the event goes to the app (before) and after.
static void tbTraceEvent(UIEvent *event, bool before) {
    if (event.type != UIEventTypeTouches) return;
    NSSet<UITouch *> *touches = event.allTouches;
    uint64_t now = tbNowNs();
    if (before) {
        bool begins = false;
        for (UITouch *t in touches) if (t.phase == UITouchPhaseBegan) begins = true;
        if (begins) {
            os_unfair_lock_lock(&tbTraceLock);
            bool fresh = !tbTraceActive || (tbTraceDown <= 0 && now - tbTraceLastLift > TB_TRACE_JOIN_NS);
            tbTraceGen++; // a pending end waits for this touch's lift
            os_unfair_lock_unlock(&tbTraceLock);
            if (fresh) tbTraceBegin(now);
        }
        if (!tbTraceIsActive()) return;
        bool moveDue = now - tbTraceLastMove >= TB_TRACE_MOVE_EVERY_NS;
        for (UITouch *t in touches) {
            UITouchPhase ph = t.phase;
            if (ph == UITouchPhaseStationary || (ph == UITouchPhaseMoved && !moveDue)) continue;
            if (ph == UITouchPhaseMoved) tbTraceLastMove = now;
            NSNumber *tid = [tbTraceTouches objectForKey:t];
            if (!tid) { tid = @(++tbTraceNextTouch); [tbTraceTouches setObject:tid forKey:t]; }
            UIWindow *w = t.window;
            CGPoint p = w ? [w convertPoint:[t locationInView:w] toCoordinateSpace:w.screen.coordinateSpace] : [t locationInView:nil];
            TBTraceEntry x = {.t = now, .kind = TBTraceTouch, .a = (uint8_t)ph, .id = tid.intValue};
            x.v[0] = p.x; x.v[1] = p.y;
            x.v[2] = (CACurrentMediaTime() - t.timestamp) * 1000; // after the system stamped it
            x.v[3] = t.tapCount;
            tbTraceAdd(&x);
            if (ph == UITouchPhaseBegan) { tbTraceDown++; tbTraceLastDown = now; tbTraceScrollsUnder(t, TBMomentDown); }
            if (ph == UITouchPhaseEnded || ph == UITouchPhaseCancelled) {
                tbTraceLifts++;
                tbTraceScrollsUnder(t, TBMomentLift);
            }
        }
        return;
    }
    int lifts = tbTraceLifts;
    tbTraceLifts = 0;
    if (!tbTraceIsActive()) return;
    for (UITouch *t in touches)
        for (UIGestureRecognizer *gr in t.gestureRecognizers) tbTraceSees(gr, now);
    if (!lifts) return;
    tbTraceScrollsUnder(nil, TBMomentRelease); // what UIKit made of the lift
    tbTraceDown -= lifts;
    if (tbTraceDown <= 0) {
        tbTraceDown = 0;
        os_unfair_lock_lock(&tbTraceLock);
        tbTraceLastLift = tbNowNs();
        uint64_t gen = ++tbTraceGen;
        os_unfair_lock_unlock(&tbTraceLock);
        tbTraceScheduleEnd(gen, TB_TRACE_TAIL_NS);
    }
}

// Whether a recognizer gets a touch of the event being delivered.
static bool tbTraceOnTouch(UIGestureRecognizer *gr) {
    for (UITouch *t in tbTraceEventNow.allTouches)
        if ([t.gestureRecognizers containsObject:gr]) return true;
    return false;
}

// Every recognizer's state change while a gesture is traced, but for the failures of those not
// on the touch (the system's own, on every touch). A failure or cancel names the recognizers
// that recognized since the last touch-down (the ones that won).
static void tbTraceSetState(UIGestureRecognizer *self, SEL _cmd, UIGestureRecognizerState state) {
    UIGestureRecognizerState from = self.state;
    tbTraceSetStateOrig(self, _cmd, state);
    if (![NSThread isMainThread] || !tbTraceIsActive()) return;
    UIGestureRecognizerState to = self.state;
    if (from == to) return;
    uint64_t now = tbNowNs();
    TBTraceRecognizerInfo *info = tbTraceRecognizerInfo(self);
    if (!info.onTouch && tbTraceOnTouch(self)) tbTraceSees(self, now);
    bool lost = to == UIGestureRecognizerStateFailed || to == UIGestureRecognizerStateCancelled;
    if (to == UIGestureRecognizerStateBegan || to == UIGestureRecognizerStateEnded) info.recognizedAt = now;
    if (lost && !info.onTouch) return;
    TBTraceEntry x = {.t = now, .kind = TBTraceRecognizer, .a = (uint8_t)from, .b = (uint8_t)to,
                      .flags = info.onTouch, .id = info.ident};
    tbTraceName(x.name, sizeof x.name, self);
    if (lost) {
        size_t used = 0;
        for (UIGestureRecognizer *other in [tbTraceRecognizers keyEnumerator].allObjects) {
            TBTraceRecognizerInfo *o = [tbTraceRecognizers objectForKey:other];
            if (other == self || !o.recognizedAt || o.recognizedAt < tbTraceLastDown || used + 8 >= sizeof x.extra) continue;
            int n = snprintf(x.extra + used, sizeof x.extra - used, "%s#%d %s", used ? ", " : "",
                             o.ident, class_getName(object_getClass(other)));
            if (n > 0) used = MIN(used + (size_t)n, sizeof x.extra - 1);
        }
    }
    tbTraceAdd(&x);
}

// Main-thread turns during a gesture: one over 50 ms is a stall; a released scroll view that
// stopped is settled.
static void tbTraceTurn(CFRunLoopObserverRef o, CFRunLoopActivity a, void *info) {
    uint64_t now = tbNowNs();
    if (a == kCFRunLoopAfterWaiting) { tbTraceTurnStart = now; return; }
    uint64_t start = tbTraceTurnStart;
    tbTraceTurnStart = 0;
    os_unfair_lock_lock(&tbTraceLock);
    bool active = tbTraceActive, awaiting = tbTraceAwaitingSettle > 0;
    os_unfair_lock_unlock(&tbTraceLock);
    if (!active) return;
    if (start && now - start > TB_TRACE_STALL_NS) {
        TBTraceEntry x = {.t = start, .kind = TBTraceStall};
        x.v[0] = (double)(now - start) / 1e6;
        tbTraceAdd(&x);
    }
    if (!awaiting) return;
    for (UIScrollView *sv in [tbTraceScrolls keyEnumerator].allObjects) {
        TBTraceScrollInfo *s = [tbTraceScrolls objectForKey:sv];
        if (!s.awaitingSettle || sv.isDecelerating || sv.isDragging) continue;
        s.awaitingSettle = false;
        os_unfair_lock_lock(&tbTraceLock);
        tbTraceAwaitingSettle--;
        os_unfair_lock_unlock(&tbTraceLock);
        tbTraceScrollEntry(sv, TBMomentSettled);
    }
}

static void tbTraceInstall(void) {
    const char *path = getenv("TB_GESTURE_TRACE");
    if (!tbTraceEnabled(path)) return;
    static TBTraceEntry storage[TB_TRACE_CAP];
    tbTraceInit(&tbTraceRing, storage, TB_TRACE_CAP);
    tbTracePath = strdup(path);
    tbTraceQueue = dispatch_queue_create("dev.testbuddy.probe.trace", DISPATCH_QUEUE_SERIAL);
    tbTraceTouches = [NSMapTable weakToStrongObjectsMapTable];
    tbTraceScrolls = [NSMapTable weakToStrongObjectsMapTable];
    tbTraceRecognizers = [NSMapTable weakToStrongObjectsMapTable];
    tbTracePageIvar = tbTraceIvar("_pageDecelerationTarget", "{CGPoint=");
    tbTraceHVelIvar = tbTraceIvar("_horizontalVelocity", "d");
    tbTraceVVelIvar = tbTraceIvar("_verticalVelocity", "d");
    tbSwizzle(UIGestureRecognizer.class, @selector(setState:), (IMP)tbTraceSetState, (IMP *)&tbTraceSetStateOrig);
    CFRunLoopObserverRef woke = CFRunLoopObserverCreate(NULL, kCFRunLoopAfterWaiting, true, INT_MIN, tbTraceTurn, NULL);
    CFRunLoopObserverRef waits = CFRunLoopObserverCreate(NULL, kCFRunLoopBeforeWaiting, true, INT_MAX, tbTraceTurn, NULL);
    CFRunLoopAddObserver(CFRunLoopGetMain(), woke, kCFRunLoopCommonModes);
    CFRunLoopAddObserver(CFRunLoopGetMain(), waits, kCFRunLoopCommonModes);
    // The app leaving: what is traced so far is written now.
    for (NSNotificationName name in @[UIApplicationDidEnterBackgroundNotification, UIApplicationWillTerminateNotification])
        [NSNotificationCenter.defaultCenter addObserverForName:name object:nil queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *n) {
            os_unfair_lock_lock(&tbTraceLock);
            tbTraceEndLocked();
            os_unfair_lock_unlock(&tbTraceLock);
        }];
    tbTraceOn = true;
}

// Every event the app is sent. The end of a touch that reached the app may make the changes
// of its turn the touch's (issue #309, TBTouchGuard.h tbSrcNoteTouch).
static void tbSendEvent(UIApplication *app, SEL cmd, UIEvent *event) {
    bool ended = false, touches = event.type == UIEventTypeTouches;
    if (touches) {
        for (UITouch *t in event.allTouches)
            if (t.phase == UITouchPhaseEnded || t.phase == UITouchPhaseCancelled) ended = true;
    }
    if (tbTraceOn) { tbTraceEvent(event, true); tbTraceEventNow = event; }
    bool delivered = tbDeliverEvent(app, cmd, event);
    if (tbTraceOn) { tbTraceEventNow = nil; tbTraceEvent(event, false); }
    if (touches) {
        os_unfair_lock_lock(&tbLock);
        tbSrcNoteTouch(&tbSrc, ended, delivered);
        os_unfair_lock_unlock(&tbLock);
    }
}

// The app's phase for the agent (issue #290, TBTouchGuard.h tbNextPhase): its state on
// dev.testbuddy.lifecycle.<pid>, which the agent reads, and a post of <tbName>.lifecycle, which
// wakes it. Input to an app still entering waits until it is active (InputReady.swift).
static int tbPhaseToken = -1;
static char tbPhaseName[96];
static TBPhase tbPhase = TBPhaseEntering;

static void tbSetPhase(TBPhase p) {
    tbPhase = p;
    if (tbPhaseToken < 0) return;
    notify_set_state(tbPhaseToken, (uint64_t)p);
    notify_post(tbPhaseName);
}

static void tbFollowLifecycle(void) {
    char perPid[96];
    snprintf(perPid, sizeof perPid, "dev.testbuddy.lifecycle.%d", getpid());
    snprintf(tbPhaseName, sizeof tbPhaseName, "%s.lifecycle", tbName);
    if (notify_register_check(perPid, &tbPhaseToken) != NOTIFY_STATUS_OK) { tbPhaseToken = -1; return; }
    NSNotificationCenter *nc = NSNotificationCenter.defaultCenter;
    NSOperationQueue *main = NSOperationQueue.mainQueue;
    NSDictionary<NSNotificationName, NSNumber *> *events = @{
        UIApplicationWillEnterForegroundNotification: @(TBLifeWillEnterForeground),
        UIApplicationDidBecomeActiveNotification: @(TBLifeDidBecomeActive),
        UIApplicationWillResignActiveNotification: @(TBLifeWillResignActive),
        UIApplicationDidEnterBackgroundNotification: @(TBLifeDidEnterBackground),
    };
    for (NSNotificationName name in events) {
        TBLifeEvent e = (TBLifeEvent)events[name].intValue;
        [nc addObserverForName:name object:nil queue:main usingBlock:^(NSNotification *n) {
            tbSetPhase(tbNextPhase(tbPhase, e));
        }];
    }
    // A process starts entering: SpringBoard animates its window in until its first activation.
    tbSetPhase(TBPhaseEntering);
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
    char request[96], result[96], point[96], rejected[96], deadline[96], frame[96];
    snprintf(request, sizeof request, "dev.testbuddy.control.request.%d", getpid());
    snprintf(result, sizeof result, "dev.testbuddy.control.result.%d", getpid());
    snprintf(point, sizeof point, "dev.testbuddy.control.point.%d", getpid());
    snprintf(rejected, sizeof rejected, "dev.testbuddy.control.rejected.%d", getpid());
    snprintf(deadline, sizeof deadline, "dev.testbuddy.control.deadline.%d", getpid());
    snprintf(frame, sizeof frame, "dev.testbuddy.control.frame.%d", getpid());
    snprintf(tbControlCompletedName, sizeof tbControlCompletedName, "dev.testbuddy.control.completed.%d", getpid());
    if (notify_register_check(request, &tbControlRequest) == NOTIFY_STATUS_OK &&
        notify_register_check(result, &tbControlResult) == NOTIFY_STATUS_OK &&
        notify_register_check(point, &tbControlPoint) == NOTIFY_STATUS_OK &&
        notify_register_check(rejected, &tbControlRejected) == NOTIFY_STATUS_OK &&
        notify_register_check(tbControlCompletedName, &tbControlCompleted) == NOTIFY_STATUS_OK &&
        notify_register_check(deadline, &tbControlDeadline) == NOTIFY_STATUS_OK) {
        if (notify_register_check(frame, &tbControlFrame) != NOTIFY_STATUS_OK) tbControlFrame = -1;
        tbSwizzle(UIApplication.class, @selector(sendEvent:), (IMP)tbSendEvent, (IMP *)&tbSendEventOrig);
        tbWitness = [TBControlWitness new];
        tbTraceInstall(); // only with TB_GESTURE_TRACE (issue #337)
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
    tbFollowLifecycle();
    // Say hello: the agent learns this pid is probed.
    notify_set_state(tbToken, ((uint64_t)(uint32_t)getpid() << 32));
    notify_post(tbName);
}
