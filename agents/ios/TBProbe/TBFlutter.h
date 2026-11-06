// Optional in-process Flutter adapter for simulator apps. The probe does not link Flutter.
#import <UIKit/UIKit.h>
#import <dlfcn.h>
#import <notify.h>

static void tbConnectFlutter(void (*mark)(id)) {
    NSNotificationName const *notification = (NSNotificationName const __unsafe_unretained *)dlsym(RTLD_DEFAULT, "FlutterSemanticsUpdateNotification");
    Class controller = NSClassFromString(@"FlutterViewController");
    Class surface = NSClassFromString(@"FlutterView");
    BOOL trace = getenv("TB_FLUTTER_TRACE") != NULL;
    if (trace) NSLog(@"TBFlutter connect notification=%d controller=%@ surface=%@", notification != NULL, controller, surface);
    if (!notification || !*notification || !controller || !surface) return;
    // A semantics-tree update wakes pending assertions even when Flutter draws without a
    // layer commit. It does not acknowledge an input or hold the app busy until another frame.
    [NSNotificationCenter.defaultCenter addObserverForName:*notification object:nil
        queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *note) {
            UIViewController *vc = note.object;
            if (![vc isKindOfClass:controller] || !vc.viewIfLoaded.window.isKeyWindow ||
                vc.presentedViewController || vc.view.window.windowScene.activationState != UISceneActivationStateForegroundActive) return;
            if (trace) NSLog(@"TBFlutter semantics");
            mark(vc.view.layer);
        }];

    // Per-process capability and request/reply. Resolve ownership on the main thread for
    // this drag, including UIKit sheets and platform views inside a Flutter app.
    __block CGRect keyboard = CGRectNull;
    NSNotificationCenter *center = NSNotificationCenter.defaultCenter;
    [center addObserverForName:UIKeyboardWillChangeFrameNotification object:nil queue:NSOperationQueue.mainQueue
        usingBlock:^(NSNotification *note) {
            keyboard = [note.userInfo[UIKeyboardFrameEndUserInfoKey] CGRectValue];
        }];
    [center addObserverForName:UIKeyboardDidHideNotification object:nil queue:NSOperationQueue.mainQueue
        usingBlock:^(NSNotification *note) { keyboard = CGRectNull; }];

    NSString *prefix = [NSString stringWithFormat:@"dev.testbuddy.flutter.%d", getpid()];
    NSString *answer = [prefix stringByAppendingString:@".answer"];
    int reply;
    if (notify_register_check(answer.UTF8String, &reply) != NOTIFY_STATUS_OK) return;
    int request;
    if (notify_register_dispatch(prefix.UTF8String, &request, dispatch_get_main_queue(), ^(int token) {
        uint64_t query = 0;
        if (notify_get_state(token, &query) != NOTIFY_STATUS_OK) return;
        CGPoint point = CGPointMake((int16_t)(query >> 16), (int16_t)query);
        BOOL owned = NO;
        UIWindow *candidate = nil;
        for (UIScene *scene in UIApplication.sharedApplication.connectedScenes) {
            if (![scene isKindOfClass:UIWindowScene.class] || scene.activationState != UISceneActivationStateForegroundActive) continue;
            UIWindow *window = ((UIWindowScene *)scene).keyWindow;
            if (!window) continue;
            // Ambiguous multiple active scenes must use normal platform input.
            if (candidate && !CGRectContainsPoint(keyboard, point)) { candidate = nil; break; }
            candidate = window;
        }
        if (candidate && !CGRectContainsPoint(keyboard, point)) {
            CGPoint local = [candidate convertPoint:point fromCoordinateSpace:candidate.screen.coordinateSpace];
            UIView *hit = [candidate hitTest:local withEvent:nil];
            if (trace) NSLog(@"TBFlutter hit %@ at %@", hit.class, NSStringFromCGPoint(local));
            owned = [hit isKindOfClass:surface];
        }
        uint32_t nonce = (uint32_t)(query >> 32);
        notify_set_state(reply, ((uint64_t)nonce << 1) | owned);
        notify_post(answer.UTF8String);
    }) == NOTIFY_STATUS_OK) {
        notify_set_state(reply, 2); // nonzero: bridge available (request nonces start above 1)
    } else {
        notify_cancel(reply);
    }
}


// Xcode debug apps load the Flutter engine from the app's debug dylib after injected
// libraries' constructors. Wait for launch before resolving its exported notification.
static void tbInstallFlutter(void (*mark)(id)) {
    NSString *framework = [NSBundle.mainBundle.privateFrameworksPath stringByAppendingPathComponent:@"Flutter.framework"];
    if (![NSFileManager.defaultManager fileExistsAtPath:framework]) return;
    [NSNotificationCenter.defaultCenter addObserverForName:UIApplicationDidFinishLaunchingNotification object:nil
        queue:nil usingBlock:^(NSNotification *note) { tbConnectFlutter(mark); }];
}
