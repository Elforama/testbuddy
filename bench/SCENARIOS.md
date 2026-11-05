# Buddy Bench scenarios

Buddy Bench opens on a **scenario list**. Each scenario is a self-contained part of the app
that puts a test runner in a known hard situation: slow screens, janky animations, system
dialogs, overlays, poor accessibility, every standard control, back stacks, and each
platform's own UI toolkits. One flow per scenario (`bench/flows/scenarios/*.yaml`) checks
all of a scenario's edge cases in one run, on Android, iOS and web, with the same ids and
texts (see [SPEC.md](SPEC.md) for the id rules and the original journey).

Rules for every scenario:
- Same ids, texts and timings on every platform, unless a row says otherwise ("Android
  only", "iOS only", "web only": the flow guards those steps with `when: platform`).
- Texts match exactly, including `…` (U+2026).
- Each scenario screen has a **Back** button (id `back`, text "Back") at the top left that
  returns to the scenario list (Android's system back and iOS's swipe-back work too).
- Everything is local and deterministic: no network. Timings are fixed (no randomness).
- State lives in memory for the app process; `launchApp: {clearState: true}` resets it.
- Portrait only. Phone sizes. The two-pane screen in "Lists" lays out two panes at any width.

## Scenario list (start screen)

| Element | Id | Text |
|---|---|---|
| Title | `scenarios_title` | "Scenarios" |
| List | `scenarios` | A vertical scrolling list of rows, one per scenario, in this order. Each row is a button: its id is `scenario_<key>`, its text the scenario's name. 15 rows: on a phone some are below the fold. |

| # | Key | Name |
|---|---|---|
| 1 | `journey` | "Journey" |
| 2 | `slow` | "Slow and janky" |
| 3 | `permissions` | "Permissions" |
| 4 | `overlays` | "Overlays" |
| 5 | `controls` | "Controls" |
| 6 | `navigation` | "Navigation and back stack" |
| 7 | `onboarding` | "Onboarding and login" |
| 8 | `lists` | "Lists" |
| 9 | `a11y_poor` | "Poor accessibility" |
| 10 | `a11y_good` | "Good accessibility" |
| 11 | `native` | "Native toolkits" |
| 12 | `animations` | "Animations" |
| 13 | `errors` | "Errors and retries" |
| 14 | `keyboard` | "Keyboard" |
| 15 | `transitions` | "Screen transitions" |

Each scenario screen's title has id `scenario_title` and the scenario's name as its text.
Scenario screens are taller than the screen where noted: they scroll as one page (a vertical
scroll view, id `page`), so flows use `scrollUntilVisible` to reach lower sections.

---

## 1. Journey

The original Buddy Bench (SPEC.md screens 1-4: Login, Home, Detail, Live), unchanged, opened
from the list. Log out returns to Login; Back on Login returns to the scenario list.

## 2. Slow and janky

Everything here is late, slow or blocks the UI thread, by fixed amounts.

| Element | Id | Behaviour |
|---|---|---|
| Staged load | `stage_text` | When the screen opens: skeleton rows (3 grey bars, ids `skeleton_1..3`, no text) for **2000 ms**, then they are replaced by the text "Loaded" (id `stage_text`) and three rows "Slow row 1", "Slow row 2", "Slow row 3" (ids `slow_row_1..3`) that appear **one at a time, 400 ms apart** (each fades in over 300 ms). |
| Late button | `late_button` | "Late button", added to the screen **3000 ms** after it opens (nothing reserves its space: content below it moves down when it appears). Tap → `late_count` "Late taps: N". |
| Late count | `late_count` | "Late taps: 0" |
| Slow open | `slow_open` | "Open slowly" → after **1200 ms** of nothing visible changing, pushes the Slow detail screen with a **slow transition: 1500 ms** slide (custom, not the platform default). |
| Slow detail | `slow_detail_title` | "Slow detail". Has Back (`back`) which pops with the same 1500 ms transition. |
| Jank button | `jank_button` | "Block UI" → blocks the main thread for **700 ms** (busy loop), then sets `jank_text` "Unblocked N" (N = taps). |
| Jank text | `jank_text` | "Not blocked" |
| Slow list | `slow_list_open` | "Slow list" → opens a screen with a list "Slow item 1"…"Slow item 20" (ids `slow_item_N`) that loads **2500 ms** after opening (a spinner `slow_list_spinner` until then), and more rows "Slow item 21"…"Slow item 40" **2500 ms** after scrolling to the end (spinner at the end meanwhile). Back returns. |

## 3. Permissions

Real runtime permission dialogs from the system. Each button requests one permission and a
status line shows the result: "<Name>: granted" / "<Name>: denied" / "<Name>: not asked".

| Element | Id | Behaviour |
|---|---|---|
| Camera | `perm_camera` / `perm_camera_status` | "Ask camera" / "Camera: not asked". Android `CAMERA`; iOS AVCaptureDevice video; web `getUserMedia({video:true})` (status "denied" when unavailable). |
| Location | `perm_location` / `perm_location_status` | "Ask location" / "Location: not asked". Android `ACCESS_FINE_LOCATION`; iOS when-in-use; web geolocation. |
| Notifications | `perm_notifications` / `perm_notifications_status` | "Ask notifications" / "Notifications: not asked". Android 13+ `POST_NOTIFICATIONS` (older: shows "Notifications: granted" at once); iOS UNUserNotificationCenter alert+badge+sound; web `Notification.requestPermission()`. |
| Contacts | `perm_contacts` / `perm_contacts_status` | "Ask contacts" / "Contacts: not asked". Android `READ_CONTACTS`; iOS CNContactStore; web: shows "Contacts: denied" at once (no such API). |
| Refresh | `perm_refresh` | "Refresh" → re-reads every status from the system (no prompt). |
| In-app rationale | `perm_rationale` | "Ask with rationale" → first shows an **in-app** dialog (id `rationale_dialog`, text "We need your microphone", buttons "Not now" `rationale_no` and "Continue" `rationale_yes`); Continue then requests the microphone (status `perm_mic_status`: "Microphone: …"). |
| Post notification | `perm_post` | "Post a notification" (Android/iOS): posts a local notification "Bench says hi" (if allowed); status `perm_post_status` "Posted" / "Not allowed". |

## 4. Overlays

Things drawn over the screen that take touches (or don't). The base screen behind them has a
button "Under" (id `under_button`) that counts taps in `under_count` "Under taps: N", placed
so every overlay below covers it.

| Element | Id | Behaviour |
|---|---|---|
| Modal dialog | `open_dialog` | "Open dialog" → a modal dialog (platform alert style: Android AlertDialog, iOS UIAlertController .alert, web `<dialog>`), title "Delete item?" (`dialog_title`), buttons "Cancel" and "Delete". Delete → `overlay_result` "Deleted"; Cancel → "Cancelled". |
| Bottom sheet | `open_sheet` | "Open sheet" → a bottom sheet (Android BottomSheetDialog-style view, iOS `.sheet` with detents, web a fixed panel) with title "Sheet" (`sheet_title`), a button "Sheet action" (`sheet_action`, → `overlay_result` "Sheet action") and a "Close" button (`sheet_close`). Dragging it down also closes it. |
| Snackbar | `show_snackbar` | "Show snackbar" → a snackbar/toast-like bar at the bottom for **3000 ms**, text "Saved" (`snackbar_text`) with an "Undo" button (`snackbar_undo`, → `overlay_result` "Undone"). It covers the bottom of the screen while shown. |
| Loading veil | `show_veil` | "Show veil" → a see-through dark overlay with a spinner over the whole screen for **1500 ms** that swallows taps. `under_button` stays visible through it (and in the accessibility tree). |
| Tooltip | `show_tooltip` | "Show tooltip" → a small popover anchored under the button, text "Tip: long press for more" (`tooltip_text`); a tap anywhere dismisses it **without** passing the tap through. |
| Full-screen cover | `open_cover` | "Open cover" → a full-screen modal (iOS `fullScreenCover`, Android a full-screen dialog, web a fixed full-page div) titled "Cover" (`cover_title`) with "Open sheet on cover" (`cover_sheet`) and "Close cover" (`cover_close`). "Open sheet on cover" presents a sheet over the cover with "Sheet on cover" (`cover_sheet_title`) and "Done" (`cover_sheet_done`, closes the sheet → `cover_result` "Done on cover" on the cover). |
| System overlay | `system_overlay` | **Android only.** "Show floating bubble" → if the app may draw over other apps (`SYSTEM_ALERT_WINDOW`; `perm_overlay_status` "Overlay: allowed/not allowed"), adds a `TYPE_APPLICATION_OVERLAY` window: a 160x160 dp circle labelled "Bubble" (content description "Bubble", id `bubble`) at the bottom right that stays over every app until tapped (tap removes it, `overlay_result` "Bubble closed"). If not allowed, opens the system "Display over other apps" settings page. |
| Result | `overlay_result` | "Nothing yet" |

## 5. Controls

Every standard control, each with a status line showing its value. The page scrolls (`page`).

| Control | Id | Status (id, initial text) |
|---|---|---|
| Tabs (3: "One", "Two", "Three") | tabs `tab_one`, `tab_two`, `tab_three` | `tabs_status` "Tab: One" |
| Segmented control (S, M, L) | `seg_s`, `seg_m`, `seg_l` | `seg_status` "Size: M" |
| Radio group (Red, Green, Blue) | `radio_red`, `radio_green`, `radio_blue` | `radio_status` "Color: none" |
| Checkboxes (Milk, Eggs) | `check_milk`, `check_eggs` | `check_status` "Basket: empty" ("Basket: Milk", "Basket: Milk, Eggs", ...) |
| Switch | `switch_wifi` (label "Wi-Fi") | `switch_status` "Wi-Fi off" |
| Slider 0–100, step 1, starts 50 | `slider_volume` | `slider_status` "Volume: 50" |
| Stepper (−/+) 0–10, starts 1 | `stepper_minus`, `stepper_plus` | `stepper_status` "Quantity: 1" |
| Dropdown / picker (Apple, Banana, Cherry) | `dropdown_fruit` | `dropdown_status` "Fruit: Apple". Android Spinner (popup list), iOS Menu-style Picker, web `<select>`. Options have texts "Apple", "Banana", "Cherry". |
| Date picker | `date_open` "Pick a date" | `date_status` "Date: none". Opens the platform date picker (Android DatePickerDialog, iOS compact DatePicker, web `<input type=date>` — web may be set by typing). Picking any date shows "Date: YYYY-MM-DD". |
| Chips (filter: Fast, Cheap, Good) | `chip_fast`, `chip_cheap`, `chip_good` | `chip_status` "Filters: none" (selected chips in that order, joined by ", ") |
| Rating (5 stars) | `star_1`..`star_5` | `rating_status` "Rating: 0" |
| Text field | `field_name` (hint "Your name") | `field_status` "Hello, nobody" → "Hello, <text>" as typed |
| Number field | `field_age` (hint "Age", numeric keyboard) | `age_status` "Age: none" |
| Password field | `field_secret` (hint "Secret", secure) | `secret_status` "Secret length: 0" |
| Multiline field | `field_notes` (hint "Notes", 3 lines tall) | `notes_status` "Lines: 0" (number of lines) |
| Search field | `field_search` (hint "Search fruit") | `search_results`: a filtered list of "Apple", "Apricot", "Banana", "Blueberry", "Cherry" (prefix match, case-insensitive), each row id `search_<lowercase name>` |
| Progress bar | `progress_bar` | determinate, `progress_status` "Progress: 0%"; button `progress_go` "Start" fills it 0→100 over **2000 ms** in steps of 10, status follows |
| Expandable section | `expand_toggle` "More options" | reveals `expand_content` "Hidden option" (a switch `switch_hidden`, label "Hidden switch", status `hidden_status` "Hidden off") |
| Disabled button | `disabled_button` "Can't tap me" | disabled; tapping it does nothing (`disabled_status` "Untouched") |
| Long-press button | `longpress_button` "Hold me" | long press (≥ 500 ms) → `longpress_status` "Held"; a plain tap → "Just tapped" |
| Double-tap area | `doubletap_box` "Double-tap me" | double tap → `doubletap_status` "Double-tapped"; single tap → "Single tap" (decided 300 ms after the last tap) |

## 6. Navigation and back stack

| Element | Id | Behaviour |
|---|---|---|
| Depth | `nav_depth` | "Depth 0" on the scenario screen |
| Push | `nav_push` | "Push" → pushes a level screen "Level N" (title id `level_title`) with its own `nav_push`, `nav_pop` "Pop", `nav_home` "Pop to root", and `nav_depth` "Depth N". Up to depth 5. |
| Pop to root | `nav_home` | returns to depth 0 |
| Tabs with own stacks | `nav_tabs` | "Tabbed screens" → a screen with a bottom tab bar, tabs `navtab_feed` "Feed", `navtab_profile` "Profile". Each tab has its own stack: "Open post" (`open_post`) pushes "Post" (`post_title`) inside Feed; switching to Profile and back to Feed shows Post still open. A "Close tabs" button (`tabs_close`) returns to the scenario screen. |
| Dialog then back | `nav_dialog` | "Open confirm" → an in-app dialog "Leave?" (`leave_dialog`, buttons "Stay" `leave_stay`, "Leave" `leave_go`). A system back (Android) closes the **dialog only**; Leave pops to the scenario list. |
| Unsaved changes | `nav_form` | "Edit form" → screen with field `form_field` (hint "Title"); going back with text in the field asks "Discard changes?" (`discard_dialog`, "Keep editing" `discard_keep`, "Discard" `discard_yes`). |
| Deep link | — | Android/iOS: the URL `bench://level/3` opens the Navigation scenario at "Level 3" with a stack 0→1→2→3 (back pops to Level 2). Web: `#/navigation/level/3`. |
| Back counter | `back_presses` | Android only: "Back presses: N" counts system back presses the scenario screen intercepted (the first back on the scenario screen is intercepted and counted: "Press back again to leave"; the second leaves). |

## 7. Onboarding and login

Opening it shows onboarding the first time per app launch (in memory).

| Element | Id | Behaviour |
|---|---|---|
| Pager | `pager` | 3 pages, swiped horizontally: titles "Welcome" (`page_1`), "Track things" (`page_2`), "Stay in sync" (`page_3`); page indicator `pager_indicator` "Page N of 3". A swipe turns one page (a paging list: a short drag springs back). |
| Skip | `onboarding_skip` | "Skip" → straight to Sign in |
| Next | `onboarding_next` | "Next" on pages 1–2; on page 3 the button reads "Get started" (`onboarding_done`) → Sign in |
| Sign in: email | `signin_email` | hint "Email" (email keyboard) |
| Sign in: password | `signin_password` | hint "Password" (secure) |
| Remember | `signin_remember` | a checkbox "Remember me" |
| Submit | `signin_submit` | "Sign in". **Disabled until both fields are non-empty.** Validation on submit: bad email (no "@") → `signin_error` "Enter a valid email"; password "wrong" → after **1000 ms** spinner (`signin_spinner`), "Wrong password" (`signin_error`); anything else → after 1000 ms → OTP. |
| OTP | `otp_1`..`otp_6` | six one-digit boxes; typing a digit moves focus to the next box; backspace in an empty box moves back. The code is "123456"; when 6 digits are in: right → "Signed in as <email>" (`signin_done`), wrong → "Wrong code" (`otp_error`) and the boxes clear. |
| Sign out | `signout` | "Sign out" → back to Sign in (onboarding not shown again in this process). |

## 8. Lists

| Element | Id | Behaviour |
|---|---|---|
| Long list | `open_long_list` | "Long list" → 500 rows "Row 1"…"Row 500" (ids `row_N`), with sticky section headers every 25 rows ("Section A", "Section B", … ids `section_A`…), 56 dp rows; tap a row → `picked` "Picked Row N" at the top. |
| Grid | `open_grid` | "Grid" → 3 columns × 40 rows of square cells "Cell 1"…"Cell 120" (`cell_N`), tap → `picked` "Picked Cell N". |
| Carousels | `open_carousels` | "Carousels" → a vertical list of 10 sections "Shelf 1"…"Shelf 10", each with a horizontal row of 15 cards "Card S-N" (`card_S_N`), cards 140 dp wide. Tap → `picked` "Picked Card S-N". |
| Two panes | `open_two_pane` | "Two panes" → master/detail side by side at any width: left pane (40% width) a list "Mail 1"…"Mail 80" (`mail_N`), right pane the selected mail ("Mail N" `mail_detail`, with long text that itself scrolls: `mail_body`, 60 lines "Line K of mail N"). The screen's centre is in the right pane. |
| Pull to refresh | `open_refresh` | "Pull to refresh" → list "Fresh 1"…"Fresh 10"; pulling down refreshes over **1000 ms** (`refresh_spinner`) and increments `refresh_count` "Refreshed N times". |
| Swipe to delete | `open_swipe_delete` | "Swipe to delete" → rows "Task 1"…"Task 8" (`task_N`); swiping a row left reveals/does delete; `delete_status` "Tasks: 8" counts rows left. |
| Reorder | `open_reorder` | "Reorder" → rows "A", "B", "C", "D", "E" (`reorder_A`…) with drag handles (`handle_A`…); long-press-drag a handle to move a row; `order_status` "Order: ABCDE" follows. |
| Infinite | `open_infinite` | "Endless feed" → rows "Post 1"…; 20 more rows load **800 ms** after reaching the end (spinner `feed_spinner`), up to Post 200. |
| Empty | `open_empty` | "Empty list" → a list with no rows and an empty-state text "Nothing here yet" (`empty_text`) and a button "Add one" (`empty_add`) that adds "New row 1", "New row 2", … |

## 9. Poor accessibility

The same small form as "Good accessibility", built badly on purpose. Flows reach things by
text, position, or `point:`.

| Element | How it is broken | Behaviour |
|---|---|---|
| Icon buttons | Three icon-only buttons (a heart, a share arrow, a trash can), drawn as images with **no** content description, no id, not marked as buttons (plain tappable views). | Each tap sets `poor_result` (this one *has* an id) to "Liked" / "Shared" / "Trashed". |
| Custom-drawn control | A 5-segment "volume bar" drawn on a canvas, one view, no accessibility children. Tapping segment K (left→right) sets `poor_result` "Volume K". | — |
| Label not linked | A text "Nickname" next to a text field with **no** hint, no label link, no id. | Typing in it shows `poor_result` "Nickname: <text>". |
| Clickable text | A plain text "Continue" that is tappable (not a button, not marked clickable for accessibility). | → `poor_result` "Continued". |
| Duplicate labels | Three rows each with a button "Edit" (no ids), rows titled "Alpha", "Beta", "Gamma". | Edit in row X → `poor_result` "Edit X". |
| Merged container | A card (full width minus margins) with title "Order 42" on top, then a row with the text "3 items" and, at the row's right end, a button "Track", inside a container that is **one accessibility element** (Android: `importantForAccessibility` on the parent with children hidden / `focusable` merge; iOS `accessibilityElements` = [] with `isAccessibilityElement = true` on the card; web `role=button` on the card with children `aria-hidden`). Only the card's merged label is exposed. | Tapping where "Track" is drawn → `poor_result` "Tracking 42". |
| Tiny target | A 12x12 dp tappable dot right of the text "Tiny:". | → `poor_result` "Tiny tapped". |
| Text in an image | An image containing the words "Secret offer" (rendered into a bitmap), no description. | Tap → `poor_result` "Offer opened". |

`poor_result` starts as "Nothing yet".

## 10. Good accessibility

The same controls as Poor accessibility, done right: every element has an id, a label, the
right role and state.

| Element | Id | Label / behaviour |
|---|---|---|
| Like / Share / Trash | `good_like`, `good_share`, `good_trash` | buttons labelled "Like", "Share", "Trash" (content descriptions / accessibility labels) → `good_result` "Liked" / "Shared" / "Trashed" |
| Volume | `good_volume` | an adjustable/slider element labelled "Volume", value "Volume K" (1–5, starts 3); increment/decrement and taps set `good_result` "Volume K" |
| Nickname | `good_nickname` | a field labelled "Nickname" (label linked) → `good_result` "Nickname: <text>" |
| Continue | `good_continue` | a button "Continue" → "Continued" |
| Edit buttons | `edit_alpha`, `edit_beta`, `edit_gamma` | buttons labelled "Edit Alpha", … → "Edit Alpha", … |
| Order card | `order_card` with child button `order_track` "Track order 42" | → "Tracking 42" |
| Tiny target | `good_tiny` "Tiny" | a 48 dp target → "Tiny tapped" |
| Offer | `good_offer` | an image with label "Secret offer" → "Offer opened" |
| Live region | `good_status` | an accessibility live region that announces "Saved" 1000 ms after `good_save` "Save" is tapped |
| Value and label (iOS) | `good_qty_minus`, `good_qty_plus`, `good_push` | iOS only: a quantity stepper whose buttons are labelled "Decrease quantity" / "Increase quantity" with the count as their accessibility value (→ `good_qty_count` "N" between them, `good_result` "Quantity N"), and a switch with its label hidden, labelled "Push notifications" (→ "Push on"/"Push off"). iOS reads such elements with the value as their text (tb issue #1). |

`good_result` starts as "Nothing yet".

## 11. Native toolkits

Each platform shows the same mini screen built with each of its UI toolkits; the scenario
screen lists the toolkits as buttons. Every mini screen has: a title (`tk_title`, the
toolkit's name), a counter button `tk_tap` "Tap" → `tk_count` "Taps: N", a switch `tk_switch`
→ `tk_switch_status` "Off"/"On", a text field `tk_field` (hint "Type here") → `tk_echo`
"Echo: <text>", a list of 30 rows "Toolkit row 1"…"Toolkit row 30" (`tk_row_N`, row 30
needs scrolling), and Back.

| Platform | Buttons (id → toolkit) |
|---|---|
| Android | `tk_views` "Android Views" (XML layouts + RecyclerView), `tk_compose` "Jetpack Compose" (LazyColumn; ids via `testTag` with `testTagsAsResourceId = true`), `tk_webview` "WebView" (a local HTML page in an Android WebView, ids as element `id`s) |
| iOS | `tk_swiftui` "SwiftUI" (List), `tk_uikit` "UIKit" (UITableView in a UIViewController, ids via `accessibilityIdentifier`), `tk_webview` "WKWebView" (a local HTML page) |
| Web | `tk_dom` "Plain DOM", `tk_shadow` "Shadow DOM" (all of the mini screen inside an open shadow root), `tk_iframe` "Iframe" (the mini screen in a same-origin iframe), `tk_canvas` "Canvas" (only the counter: a canvas with a drawn "Tap" button at its centre; `tk_count` stays a DOM text) |

## 12. Animations

| Element | Id | Behaviour |
|---|---|---|
| Spinner | `anim_spinner` | indeterminate, forever |
| Marquee | `anim_marquee` | a text "Breaking news: everything is fine" scrolling horizontally forever |
| Pulse | `anim_pulse` | a button "Pulse" that scales 1.0→1.15→1.0 every 800 ms forever; tap → `anim_status` "Pulse tapped" |
| Spring in | `anim_spring_open` | "Spring in" → a button "Sprung" (`anim_sprung`) springs in from off-screen right (overshoots and settles over ~900 ms); tap → `anim_status` "Sprung tapped" |
| Fade in late | `anim_fade_open` | "Fade in" → "Faded" (`anim_faded`) fades in from alpha 0 over **1500 ms** (present in the tree from the start, invisible at first); tap → "Faded tapped" |
| Layout shift | `anim_shift_open` | "Shift" → after 600 ms a 200 dp banner animates in above `anim_target` "Target", pushing it down over 400 ms; tap target → "Target tapped" |
| Auto carousel | `anim_carousel` | 5 pages "Slide 1"…"Slide 5" (`slide_N`) auto-advancing every **1500 ms** forever; `anim_carousel_tap` "Stop" stops it and shows `anim_status` "Stopped at Slide N" |
| Progress | `anim_progress` | a determinate bar that fills over 4000 ms, then loops |
| Confetti | `anim_confetti` | "Celebrate" → 60 particles fall over 2500 ms over the whole screen (not touchable); during it, `anim_under` "Under confetti" must still take taps → "Under tapped" |
| Status | `anim_status` | "Idle" |

## 13. Errors and retries

| Element | Id | Behaviour |
|---|---|---|
| Flaky load | `err_load` | "Load data" → spinner 800 ms, then **fails the first time** with `err_message` "Couldn't load. Try again." and a "Retry" button (`err_retry`); the retry succeeds after 800 ms: `err_message` "Loaded 3 items". |
| Form errors | `err_form_submit` | a form with `err_email` (hint "Email") and `err_age` (hint "Age"); Submit validates: inline errors under each field (`err_email_error` "Invalid email", `err_age_error` "Must be 18 or older"); fixing both and submitting → `err_form_status` "Form sent". |
| Empty state | `err_empty` | "Show empty" → `err_message` "No results" with an illustration (decorative, no text). |
| Timeout | `err_timeout` | "Slow request" → spinner for **6000 ms**, then `err_message` "Request timed out". (Longer than tb's 5 s settle default: the screen keeps a spinner going.) |
| Error toast | `err_toast` | "Fail quietly" → a toast/snackbar "Something went wrong" (`err_toast_text`) for 2000 ms (Android: a real `Toast` — not in the accessibility tree; iOS/web: an in-app snackbar). |
| Crash-free ANR | `err_freeze` | **Android only.** "Freeze 6 s" → blocks the main thread for 6000 ms (long enough for the system's "isn't responding" dialog on many devices). Afterwards `err_message` "Thawed". |

## 14. Keyboard

| Element | Id | Behaviour |
|---|---|---|
| Bottom field | `kb_bottom` | a text field (hint "Bottom field") pinned near the bottom of the screen so the soft keyboard covers it unless the screen adjusts; typing → `kb_status` "Bottom: <text>". The screen does **not** resize for the keyboard (Android `adjustNothing`, iOS ignores the keyboard safe area, web fixed position). |
| Below-keyboard button | `kb_submit` | "Submit" right under `kb_bottom` (covered by the keyboard while it is up) → `kb_status` "Submitted" |
| Next chain | `kb_first`, `kb_second`, `kb_third` | hints "First", "Second", "Third"; the IME action is Next (Return on iOS) moving focus to the next field; on Third it is Done, which hides the keyboard and sets `kb_status` "Chain done: <first>/<second>/<third>". |
| Autofocus | `kb_autofocus_open` | "Open autofocus" → a screen whose field `kb_auto` (hint "Focused at start") has focus and the keyboard up as soon as it opens; Back returns. |
| Multiline | `kb_multiline` | hint "Long text"; Enter inserts a newline (does not submit); `kb_lines` "Lines: N" |
| Key field | `kb_keys` | hint "Key field"; its own key handler counts the Backspace keys it gets (Android an `OnKeyListener` on `KEYCODE_DEL` key-downs, iOS the field's `deleteBackward`, web `keydown` with `key === "Backspace"`): `kb_backspaces` "Backspaces: N" (starts "Backspaces: 0"). Each Backspace still deletes a character as usual. |
| Status | `kb_status` | "Nothing typed" |

## 15. Screen transitions

A screen that closes over the next one while the app is busy: it is already gone for
accessibility (what is under it is listed at once), but it stays drawn, perfectly still, and
still takes touches until the app gets to remove it (a JavaScript stack closing a screen while
its JavaScript thread is busy). Nothing on screen moves or redraws meanwhile, so a runner that
waits only for the screen to go still, then taps the next screen as soon as it is listed, taps
the closing one.

| Element | Id | Behaviour |
|---|---|---|
| Choose account | `tr_pick` | "Choose account" → opens the account list over the form, at once (no animation). |
| Account list | `tr_list` | Covers the whole screen under the title bar (from the top of the form to the bottom of the screen): rows "Checking" (`tr_account_checking`), "Savings" (`tr_account_savings`), each 64 dp/pt high, then "Credit card" (`tr_account_credit`), which fills the rest of the list's height. A row tapped picks that account and **closes the list**: from that moment the list is out of the accessibility tree (the form is listed; on Android it must not be listed even with not-important views included, e.g. drawn from the parent's overlay), but for **800 ms** it stays drawn exactly as it was (no fade, no animation, nothing redrawn) and still takes touches: a touch on it in that time picks the row under the touch (on the form's lower part, "Credit card") and starts the 800 ms again. Then the list is removed in one step. |
| Account | `tr_account` | "To: none", then "To: <account>" |
| Amount | `tr_amount` | a number field, hint "Amount", below the list's first two rows (so under "Credit card" while the list fades) |
| Summary | `tr_summary` | "Send <amount> to <account>"; "Send nothing to <account>" while the amount is empty (account "none" before a pick) |
