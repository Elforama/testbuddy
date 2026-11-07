# Everyday

A Flutter demo for ordinary end-to-end journeys on Android and iOS.
The application ID is `dev.testbuddy.everyday` on both platforms.

Any email and password, including empty values, signs in after 500 ms.
All data stays in memory. Signing out starts a fresh demo session.

- My list starts with three items. Add item opens a text dialog. OK appends a
  nonempty item, and Cancel dismisses the dialog. Each item has a completion checkbox.
- Bottom navigation opens My list, Settings and Profile.
- Settings includes three switches, three topic checkboxes, a radio group, a note
  field and a save button.
- Profile includes name and city fields, a terms checkbox and a save button.
- The hamburger menu also opens Help, About and Sign out.

Build from this directory with `flutter build apk --debug` and
`flutter build ios --debug --simulator`. Run `flutter analyze` and `flutter test`
for local checks.

After installing the app, run the supplied flow from the repository root:

```sh
bin/tb test bench/everyday/flows/journey.yaml -p android --device emulator-5554
bin/tb test bench/everyday/flows/journey.yaml -p ios --device "$IOS_UDID"
```
