# Everyday native app runner comparison, 6 November 2026

One fresh run per runner and platform, using the same shared Everyday journey and installed native app build. Android uses Jetpack Compose and iOS uses SwiftUI. Each platform ran TestBuddy, Speedrun, then Maestro sequentially. The Android emulator and iOS simulator ran concurrently on one Mac. All tools used their default runtime settings and zero retries.

TestBuddy 0.3.0, Speedrun 0.4.7 and Maestro 2.12.1 were tested on emulator-5554 (Android 16 / API 36) and the TestBuddy Suite iPhone 17 simulator (iOS 26.4). The app ID is dev.testbuddy.everyday.nativeapp. The shared flow accepts the app ID through the EVERYDAY variable.

| Platform | Runner | Result | Flow time | Total command time |
|---|---|---|---:|---:|
| android | TestBuddy | Passed | 61.669 s | 66.448 s |
| android | Speedrun | Passed | 46.360 s | 49.581 s |
| android | Maestro | Passed | 135.135 s | 155.171 s |
| ios | TestBuddy | Passed | 34.678 s | 48.565 s |
| ios | Speedrun | Failed | 7.989 s | 14.282 s |
| ios | Maestro | Failed | 20.406 s | 41.018 s |

Flow time comes from each runner’s JUnit testcase. Total command time includes startup and reporting. Failed flows end early, so their elapsed time does not measure completion speed.

On Android, Speedrun completed the flow in 46.4 s versus TestBuddy’s 61.7 s (24.8% less elapsed flow time). Maestro took 135.1 s, or 2.19× TestBuddy’s flow time.

On iOS, Speedrun and Maestro both stopped at the first hideKeyboard, after entering the login password and before submitting login. TestBuddy completed the journey. Each failed run is retained with its original failure and logs.

This is a single observation for each runner and platform. It does not establish reliability or separate startup caching, host load and runner overhead. All original assertions were retained.

[Machine-readable results](everyday-native-runners-2026-11-06.json) · [App and shared flow instructions](../everyday-native/README.md)
