# Flow format

tb runs Maestro's YAML flow format: existing Maestro flows run unchanged. A flow is a YAML
file: a config block, a `---` line, then a list of steps.

```yaml
appId: com.example.app            # Android package / iOS bundle id; web flows use  url: https://...
name: Checkout                    # optional; default: the file name
tags: [smoke, checkout]
env:
  USER: ${USER || 'qa@example.com'}   # a default that -e USER=... can override
onFlowStart:
  - runFlow: setup.yaml
onFlowComplete:
  - takeScreenshot: end
---
- launchApp
- tapOn: "Cart"
```

`tb check <files>` validates syntax (and the sub-flows they run) without a device.

## Selectors

A bare string is a text selector: `tapOn: "Sign in"`. A map combines fields:

| Field | Matches |
|---|---|
| `text` | Visible text or accessibility label. A **regular expression matched against the whole label** (`"Welcome.*"`, `"Item 3"` does not match "Item 30"). |
| `id` | Android resource id / iOS accessibility identifier / web `id` (regex, whole id). |
| `index` | The Nth match (0-based) in screen order. |
| `enabled`, `checked`, `selected`, `focused` | State. |
| `below`, `above`, `leftOf`, `rightOf` | Relative to another selector. |
| `childOf`, `containsChild`, `containsDescendants` | Tree relations. |
| `width`, `height`, `tolerance` | Size. |
| `css` | Web only: a CSS selector. |

`point: "50%,90%"` (or pixels `"540,1800"`) taps a screen position; with a selector it is
relative to the element. Prefer selectors.

## Steps

**App**: `launchApp` (`appId`, `clearState`, `clearKeychain`, `stopApp`, `permissions:
{all: allow, camera: deny, notifications: unset, ...}`, `arguments`), `stopApp`, `killApp`,
`clearState`, `clearKeychain`, `openLink` (`link`, `autoVerify`, `browser`), `setPermissions`,
`addMedia`.

**Taps**: `tapOn`, `doubleTapOn`, `longPressOn` (selector or `point`; `repeat`, `delay`,
`retryTapIfNoChange`, `waitToSettleTimeoutMs`, `optional`).

**Text**: `inputText`, `inputRandomText` / `Number` / `Email` / `PersonName` / `CityName` /
`CountryName` / `ColorName`, `eraseText` (N characters), `copyTextFrom`, `pasteText`,
`setClipboard`, `hideKeyboard`.

**Keys**: `pressKey: Enter | Backspace | Back | Home | Lock | Tab | Escape | Power | Volume Up |
Volume Down | Remote ...`, `back` (Android's back key). iOS has no system back key: `back` does
nothing there and tb says so with a hint (in the step log and `tb do`'s output each time, once per
flow on the console). `pressKey: Back` is the same on iOS: the device refuses it ("iOS has no back
key"), the step passes, and tb gives the same hint (as for any key iOS lacks). To go back on iOS,
tap the screen's back button: `tapOn: {id: BackButton}` for the navigation bar's system one.

**Assertions and waits**: `assertVisible`, `assertNotVisible`, `assertTrue: ${expr}`,
`extendedWaitUntil: {visible: ..., timeout: 10000}` (or `notVisible`),
`waitForAnimationToEnd`, `assertScreenshot`.

**Scrolling**: `scroll`, `scrollUntilVisible: {element: ..., direction: DOWN|UP|LEFT|RIGHT,
timeout, visibilityPercentage, centerElement}`, `swipe` (`direction`, or `start`/`end`
points, or `from` a selector), `travel: {points: ["lat,lon", ...], speed}` (moves the mock
location; `speed` is metres per second, a number above 0, default 4; a latitude is a number
from -90 to 90 and a longitude from -180 to 180, here and in `setLocation`).

**A list inside the page** (tb only; a shelf of cards, a pane): `scrollUntilVisibleIn: {list:
<any item of that list>, element: ..., direction: RIGHT}` runs two steps: the page scrolls
(`pageDirection`, default DOWN) until the `list` item is whole on screen, then the list
holding it scrolls (`direction`) until `element` shows. Prefer it to scrolling to the list's
heading first: on a short screen the heading can show while the list is still below the edge.
Takes `timeout`, `speed`, `visibilityPercentage`, `centerElement`, `label`, `optional`.

**Network mocks** (tb only; web flows): `mockNetwork: {url, method, status, body | file,
headers, delay, label, optional}` answers matching requests without a server (`url` is a
Chrome URL pattern: `*` any run, `?` one character, `\` escapes; `status` default 200;
`delay` in ms; `file` relative to the flow, sent as is). `mockNetwork: {url, fail: true}` fails
the request like a network error. The latest matching mock wins; `method` compares without
case. `clearNetworkMocks` drops them all (requests held for a delay go on to the network). A
flow starts and ends with no mocks; a `tb do` session keeps them until cleared. Mocks cover the
page, the tabs it opens (from their first request) and their cross-origin iframes, not web
workers. `${}` works in url, method, body and
header values; for a JSON body use one expression (`body: '${JSON.stringify({items: []})}'`)
or `file:`, since `${}` inside JSON braces does not parse. Without a `Content-Type` header the
body is sent as `application/json` (a `.json` file or a JSON body), else `text/plain`. A
cross-origin request gets CORS headers added (its origin echoed, credentials allowed, the
mock's headers exposed) unless the mock sets its own `Access-Control-Allow-Origin`. Its
`OPTIONS` preflight is answered at once (no `delay`) when a mock covers the URL for the method
the preflight asks about; a preflight for an unmocked method goes on to the network. On
Android and iOS the step fails ("mockNetwork works only in web flows").

**JavaScript dialogs** (web): `alert`, `confirm`, `prompt` and a `beforeunload` "Leave site?"
are answered as they open (OK; a prompt with its default text; leave the page), and the trace
names each; a flow does not tap them. `answerDialogs` (tb only) chooses the answer for the
dialogs that open from then on, in every tab: `answerDialogs: CANCEL` (`confirm()` returns
false, `prompt()` null), `answerDialogs: {text: "Grace"}` (OK, `prompt()` returns the text;
`${}` works; `text` with `button: CANCEL` is an error), `answerDialogs: OK` (the default again).
The map form is `{button: OK|CANCEL, text, label, optional}`: no `button` means OK, and
`button` takes `${}` too. An alert gets OK and a `beforeunload` is left whatever the
choice. A flow starts and ends with the default; a `tb do` session keeps its choice. On Android
and iOS the step fails ("answerDialogs works only in web flows").
`assertDialog` (tb only) checks that a dialog opened since the last step that acted on the
page (checks in between, `assertDialog` included, do not move that window; one a timer opens
is waited for; put it before the next action): `assertDialog: "Delete it?"` (its message,
matched like an element's text, `${}` works), `assertDialog: {text: "Delete .*", type:
confirm}` (type: alert, confirm, prompt or beforeunload; `label`, `optional`), bare
`assertDialog` for any dialog. Frames and other tabs count. A failure lists the dialogs that
did open. `tb do` / `tb look` print each dialog tb answered as a `note:` line (`notes` in
`--json`).

**New tabs** (web): a tap on a `target=_blank` link, or on a button that calls `window.open`,
moves the flow to the new tab, as a person's attention would: the next steps look and tap
there. When that tab closes (the page closes it, or a tap on its own close button), the flow
goes back to the tab that opened it. `launchApp` closes every tab but the first.

**Device**: `setLocation`, `setOrientation`, `setAirplaneMode`, `toggleAirplaneMode`,
`setDarkMode`, `toggleDarkMode`, `assertDarkMode`, `assertLightMode`, `takeScreenshot`,
`startRecording`, `stopRecording` (the web needs `ffmpeg` to record; without it the flow goes on
unrecorded).

**Structure**: `runFlow` (`file` or inline `commands`, `env`, `when`), `repeat` (`times` or
`while`), `retry` (`maxRetries`), `evalScript`, `runScript` (`file`, `env`). Any step takes
`label:` and `optional: true` (a failure becomes a warning). `repeat`'s `times` must be a
number of 0 or more (a fraction is cut: `2.5` runs twice; `0` skips); anything else fails the step.
`retry`'s `maxRetries` likewise (cut; `0` runs once with no retry; at most 3; default 1).
`retry` runs its steps again after a failed step or a script that throws (a `runScript` that
throws until a backend is ready polls it; `output` carries across attempts), but not after a
script that does not compile.

**Not supported**: the AI-based assertion and extraction steps.

## Conditions

`runFlow: {when: {visible: "Accept"}, file: accept.yaml}`. `when:` takes `visible`,
`notVisible`, `true: ${expr}` and `platform: Android|iOS|Web`, combined with AND. Conditions are
checked on a settled screen.

## JavaScript

- `${...}` anywhere in a value is evaluated: `${USER}`, `${output.code}`,
  `${Number(COUNT) + 1}`.
- Variables (`env:`, `-e`, a calling `runFlow`'s `env:`) are globals.
- `output` is an object shared by the whole run (and by every device in a multi-device flow).
- `evalScript: ${output.n = 1}` for one-liners; `runScript: script.js` for files.
- `setTimeout` / `setInterval` (and `clear...`) work: timers run after the script's own code,
  in due order, before the step ends (up to 30 s of them).
- `http.get/post/put/delete(url, {headers, body})` returns `{ok, status, body, headers}`;
  `json(text)` parses; `faker` generates data (`faker.name().firstName()`, ...);
  `console.log` prints.
- `tb.copiedText` is the text the last `copyTextFrom` took; `tb.platform` is `android`, `ios`
  or `web`.
- Maestro flows run unchanged: the same object is also `maestro` (`maestro.copiedText`,
  `maestro.platform`), and Maestro's variable and folder names work too.

## Workspaces

Running a folder uses its `config.yaml`:

```yaml
flows: ["*", "auth/**", "!wip/*"]       # globs: * one level, ** any depth, ! excludes
includeTags: [smoke]
excludeTags: [slow]
executionOrder:
  continueOnFailure: false
  flowsOrder: [login, checkout]          # run these first, in order, by flow name
```

Files without a `---` steps section, and `config.yaml` itself, are not flows. Hidden folders
are skipped, except a `.tb` (or Maestro's `.maestro`) flows folder.

## Reports

`--format junit` (one test case per flow; `properties` in a flow's config become JUnit
properties; a flow skipped after a failed ordered flow gets `<skipped message="...">` and counts in the suite's `skipped`), `--format html` or `html-detailed` (every step; skipped flows are counted apart, with the reason). Failed flows keep a screenshot,
the screen listing, the hierarchy and every step's result in `--debug-output`.
