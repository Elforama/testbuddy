# Everyday native apps

Native versions of the Everyday Flutter demo, built with Jetpack Compose on Android
and SwiftUI on iOS. Both use application ID `dev.testbuddy.everyday.nativeapp`.
They install alongside the Flutter edition.

Both accept any login credentials (including empty values) after a 500 ms loading
state. Data is held in memory and resets on sign-out. They include the same item
dialog, completion checkboxes, navigation, settings, profile, Help and About screens.
The iOS hamburger menu opens a standard sheet. Android uses a navigation drawer.

Build Android from `bench/everyday-native/android` with
`ANDROID_HOME=<sdk> ./gradlew assembleDebug`.

Build iOS from the repository root:

```sh
xcodebuild -project bench/everyday-native/ios/EverydayNative.xcodeproj \
  -scheme EverydayNative -configuration Debug -sdk iphonesimulator \
  -destination 'generic/platform=iOS Simulator' \
  -derivedDataPath build/everyday-native/ios CODE_SIGNING_ALLOWED=NO build
```

After installation, run the shared journey:

```sh
bin/tb test bench/everyday/flows/journey.yaml -p android --device emulator-5554 \
  -e EVERYDAY=dev.testbuddy.everyday.nativeapp
bin/tb test bench/everyday/flows/journey.yaml -p ios --device "$IOS_UDID" \
  -e EVERYDAY=dev.testbuddy.everyday.nativeapp
```

The shared TestBuddy journey passed on both final builds on 6 November 2026.
Android completed the flow in 51.800 seconds, and iOS in 33.563 seconds.
The flow verifies login, adding an item, checkbox and switch states, radio
selection, text entry, saving, both menu pages, list retention and sign-out.
The iOS item-entry dialog uses a standard modal sheet with Cancel and OK.
Local JUnit reports and failure-debug history are under `build/everyday-native/`.
