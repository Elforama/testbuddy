# Flow format

A flow is a YAML file: a config block, a `---` line, then a list of steps.

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
Volume Down | Remote ...`, `back` (Android; a no-op on iOS).

**Assertions and waits**: `assertVisible`, `assertNotVisible`, `assertTrue: ${expr}`,
`extendedWaitUntil: {visible: ..., timeout: 10000}` (or `notVisible`),
`waitForAnimationToEnd`, `assertScreenshot`.

**Scrolling**: `scroll`, `scrollUntilVisible: {element: ..., direction: DOWN|UP|LEFT|RIGHT,
timeout, visibilityPercentage, centerElement}`, `swipe` (`direction`, or `start`/`end`
points, or `from` a selector), `travel`.

**Device**: `setLocation`, `setOrientation`, `setAirplaneMode`, `toggleAirplaneMode`,
`setDarkMode`, `toggleDarkMode`, `assertDarkMode`, `assertLightMode`, `takeScreenshot`,
`startRecording`, `stopRecording`.

**Structure**: `runFlow` (`file` or inline `commands`, `env`, `when`), `repeat` (`times` or
`while`), `retry` (`maxRetries`), `evalScript`, `runScript` (`file`, `env`). Any step takes
`label:` and `optional: true` (a failure becomes a warning).

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
- `http.get/post/put/delete(url, {headers, body})` returns `{ok, status, body, headers}`;
  `json(text)` parses; `faker` generates data (`faker.name().firstName()`, ...);
  `console.log` prints.
- `tb.copiedText` is the text the last `copyTextFrom` took; `tb.platform` is `android`, `ios`
  or `web`.
- Flows written for other runners keep working: tb also accepts their names for this object,
  for the variables below and for the flows folder.

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
are skipped, except a `.tb` flows folder.

## Reports

`--format junit` (one test case per flow; `properties` in a flow's config become JUnit
properties), `--format html` or `html-detailed` (every step). Failed flows keep a screenshot,
the screen listing, the hierarchy and every step's result in `--debug-output`.
