# Flutter adapter

A package can contain both Flutter and native screens. To inspect a built app,
run `python3 adapters/flutter/detect.py <app>` on a simulator .app, IPA or
Android APK. For a split Android installation pass the base and ABI APKs
together. Exit status 0 means the engine and assets were found. Status 1 means
the package did not match and 2 means inspection failed. Package detection is
a build check. Input routing needs evidence about the current view.

## iOS simulator

The existing TestBuddy simulator probe enables the adapter when the app bundles
Flutter.framework and loads the Flutter view classes and semantics notification.
The notification marks the visible controller's layer for the existing coalesced
change-event stream. This wakes pending checks, including changes that Flutter
draws without a corresponding UIKit view change.

For an automatic scroll, the agent asks the app to hit-test the starting point
on its main thread. A Flutter surface gets a drag ending at 80 points per second.
For Flutter lists that XCTest reports as an empty scroll view followed by its
rows, the agent restores the row ancestry when all the sibling bounds fit the
scroll region. The rows and labels come from the same XCTest snapshot.

The normal gesture is used when a native view owns the point, the software
keyboard covers it, multiple app windows make it ambiguous, the probe is
disabled, or the probe cannot answer promptly. Explicit swipes keep their
ordinary fling behavior. A semantics event supplies a reason to re-read the
screen. Only the requested element or result satisfies an assertion.

## Android

Android's accessibility events already wake TestBuddy. The optional test-build
hook under `android/` also signals when Flutter starts displaying a FlutterView.
Add that directory to the Android test build's sources. In a FlutterActivity
that uses the standard FlutterView, wire the engine callback as follows:

```kotlin
override fun onFlutterUiDisplayed() {
    super.onFlutterUiDisplayed()
    findViewById<FlutterView>(FLUTTER_VIEW_ID)?.let(FlutterDisplayBridge::displayed)
}
```

The hook checks that the actual FlutterView has rendered and is attached and
visible, then sends a subtree content-change event through the OS accessibility
channel. This event can wake a check. A later screen or a completed tap still
needs its own assertion. Apps without the hook continue to use the standard
Android agent. The benchmark includes the hook in its debug APK.

## Tests

Run `python3 -m unittest discover -s adapters/flutter` for the package checks.
On a simulator with the Flutter benchmark installed, run the mixed-surface test
with `TB_IOS_SIM=1 TB_FLUTTER_BENCH=1 TB_IOS_UDID=<udid> go test ./internal/driver/ios -run '^TestFlutterSurfaceRouting$'`.
It exercises the Flutter menu, a native UIKit screen in the same process, and
the return to Flutter after dismissal.
