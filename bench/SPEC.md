# Buddy Bench: the test app

One small app, built three times (Android, iOS, web) with the **same screens, texts, ids and
timings**, so one set of flows runs on every platform and benchmarks compare like
with like. It deliberately includes UI that never stops moving (spinners, tickers, a
stopwatch) and slow loads, because a test runner must still act while a screen is never
quiet.

| Platform | App id | Source |
|---|---|---|
| Android | `dev.testbuddy.bench` | `bench/android` (Kotlin, Android Views, no third-party libraries) |
| iOS | `dev.testbuddy.bench` | `bench/ios` (SwiftUI) |
| Web | `http://127.0.0.1:7360/` | `bench/web` (HTML + plain JS, served by `go run ./bench/web`) |

## Ids

An "id" is what an `id:` selector matches:
- Android: the view's `android:id` (resource id `dev.testbuddy.bench:id/<id>`; an `id:` selector also matches the part after `/`).
- iOS: `accessibilityIdentifier`.
- Web: the element's `id` attribute.

Texts must match **exactly** (case and punctuation), including `…` (U+2026) where shown.

## Timing constants (identical on every platform)

| Name | Value |
|---|---|
| Sign-in delay | 1000 ms |
| Load more delay | 800 ms |
| Reviews delay | 600 ms |
| Ticker interval | 100 ms |
| Stopwatch | every frame |
| Screen transition (push/pop) | the platform's default animation (Android: ~300 ms; iOS: navigation push; web: 250 ms CSS slide) |

## Screens

### 1. Login (scenario "Journey")

| Element | Id | Text / behaviour |
|---|---|---|
| Title | `title` | "Buddy Bench" |
| Logo spinner | `logo_spinner` | A spinner/rotating shape that **animates forever** (indeterminate progress indicator or a rotation animation). Never stops. |
| Username field | `username` | Text field, placeholder "Username" |
| Password field | `password` | Secure text field, placeholder "Password" |
| Log in button | `login` | "Log in" |
| Error text | `error` | Hidden. If either field is empty when Log in is tapped: shows "Enter a username and password". |
| Loading overlay | `loading` | After Log in with both fields filled: an overlay over the screen with an indeterminate spinner (animating forever while shown) and the text "Signing in…" (id `loading_text`). After **1000 ms** it disappears and Home opens. |

### 2. Home

| Element | Id | Text / behaviour |
|---|---|---|
| Welcome | `welcome` | "Welcome, <username>" (the username typed at login) |
| Cart count | `cart_count` | "Cart: N" (N starts at 0; shared across screens) |
| Sync spinner | `home_spinner` | Indeterminate spinner that **animates forever**, next to the text "Syncing…" (id `sync_label`). |
| Ticker | `ticker` | Text "Live: N", N += 1 every **100 ms**, forever (content that never stops changing). It keeps counting while another screen is pushed over Home, but only redraws while Home is visible. |
| Live button | `open_live` | "Live" → opens the Live screen |
| Log out | `logout` | "Log out" → back to Login (fields cleared) |
| List | `items` | Scrolling vertical list. Rows "Item 1" … "Item 40", one per row, each row tall enough that only ~10–12 rows fit on a phone screen (row height ≈ 64 pt/dp/px). Tapping a row opens its Detail screen. |
| Load more | `load_more` | Last element inside the list, after "Item 40": a button "Load more". Tap → the button is replaced by a spinner (id `load_more_spinner`) for **800 ms**, then rows "Item 41" … "Item 50" are appended and the Load more button comes back at the end. |

The header (welcome, cart count, sync spinner, ticker, Live, Log out) stays fixed at the top;
only the list scrolls.

### 3. Detail (pushed with the platform's normal screen transition)

| Element | Id | Text / behaviour |
|---|---|---|
| Title | `detail_title` | "Item N details" |
| Description | `detail_text` | "This is item N." |
| Add to cart | `add_to_cart` | "Add to cart" → cart count +1 |
| In cart | `detail_cart` | "In cart: N" (the shared cart count, updated immediately) |
| Reviews spinner | `reviews_spinner` | Shown for **600 ms** after the screen opens (animating), then replaced by: |
| Reviews | `reviews` | "4.8 stars" |
| Back | `back` | "Back" → pops to Home (Home keeps its scroll position and cart count) |

### 4. Live (stress screen: nothing here ever stops moving)

| Element | Id | Text / behaviour |
|---|---|---|
| Title | `live_title` | "Live" |
| Stopwatch | `stopwatch` | "00:00.000"-style elapsed time since the screen opened, updated **every frame** (Choreographer / TimelineView(.animation) / requestAnimationFrame). |
| Ball | `ball` | A small circle moving back and forth forever (an infinite animation). |
| Tap me | `tap_me` | "Tap me" → increments a counter |
| Tap count | `tap_count` | "Tapped N" (starts at "Tapped 0") |
| Switch | `notify_switch` | A toggle switch, off at start |
| Switch label | `notify_label` | "Notifications off" / "Notifications on", following the switch |
| Back | `back` | "Back" → pops to Home |

## Rules

- The app opens on the **scenario list** ([SCENARIOS.md](SCENARIOS.md)); the screens above are
  scenario 1, "Journey" (`scenario_journey`). Back on Login returns to the list.
- No network access, no analytics. Everything is local and deterministic. Libraries: only the
  platform's own (Android: AndroidX and Jetpack Compose from Google's Maven; iOS: Apple's
  frameworks; web: none).
- `launchApp: {clearState: true}` must reset everything (Android/iOS: app data; web: localStorage + the page reload). Keep state in memory only (cart count lives for the app process / page).
- The Journey screens use no system dialogs and no permissions; the Permissions and Overlays
  scenarios do, on purpose.
- Portrait only.
