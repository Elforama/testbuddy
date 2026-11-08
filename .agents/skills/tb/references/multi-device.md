# Multi-device flows (several devices in one flow file)

One YAML file can drive several devices at once: a chat between two users, a hand-off from
a phone to a tablet, an iPhone and an Android phone in the same test. The file has the same
shape as any flow (config, `---`, steps), and every step inside a role block is an ordinary
step.

```yaml
# chat.yaml
name: Alice messages Bob
devices:                        # this key makes it a multi-device flow
  alice:
    platform: ios               # android | ios | web | omit = any free device
    appId: com.example.chat     # default for this device's launchApp, stopApp, ...
    env: { USER: alice@example.com }
  bob:
    platform: android
    device: "Pixel 7"           # optional: pin a serial, UDID or device name
    appId: com.example.chat
    env: { USER: bob@example.com }
env:                            # shared env
  MESSAGE: "Hi Bob"
---
- parallel:                     # these blocks run at the same time; waits for all
    alice:
      - launchApp: { clearState: true }
      - runFlow: login.yaml     # existing sub-flows, unchanged
    bob:
      - launchApp: { clearState: true }
      - runFlow: login.yaml

- alice:                        # one device, in order
    - tapOn: "New message"
    - inputText: ${MESSAGE}
    - tapOn: "Send"

- bob:
    - assertVisible: ${MESSAGE}
```

Run it like any flow: `tb test chat.yaml`. It can sit in a folder with ordinary flows and
be run and sharded with them.

## Keeping devices in step, passing values

```yaml
- parallel:
    alice:
      - copyTextFrom: { id: invite_code }
      - evalScript: ${output.code = tb.copiedText}
      - signal: code-ready              # wakes everyone waiting on it
    bob:
      - waitForSignal: code-ready       # or { name: code-ready, timeout: 30000 }
      - inputText: ${output.code}
```

`waitForSignal` waits on an event (a channel closing), not a loop. Its default timeout is
120 s. Raising a signal twice is harmless; waiting on one already raised returns at once.

## Rules

| What | How it works |
|---|---|
| `devices:` | Names the roles. A role is a platform name (`bob: android`) or a map with `platform`, `device`, `appId`, `env`. A web role's `appId` is its URL; each web role gets its own browser. A role cannot be named like a step (`tapOn`, `back`, ...). |
| `- <role>: [commands]` | Runs steps on that device, in order. A single command works too: `- bob: { assertVisible: Hi }`. |
| `parallel:` | Maps roles to command lists that run at the same time. It waits for all; the first failure stops the others. |
| `signal: name` / `waitForSignal: name` | Sync devices. Usable anywhere inside a role's commands, including sub-flows. |
| `output` | One object shared by every device. |
| env | Each device sees the shared `env:`, then its role's `env:`. `${TB_ROLE}` is the role name. |
| `tb.platform`, `tb.copiedText` | Per device. |
| Platform differences | `when: { platform: iOS }` inside shared sub-flows. |
| `onFlowStart` / `onFlowComplete` | Run on every device at the same time. |
| Commands outside a role block | Allowed only when they do not touch a device (`evalScript`, `runScript`, `assertTrue`, `setClipboard`, `signal`, `waitForSignal`), or when the flow has one role. Otherwise the error says which device to name. |

## Choosing devices

`tb test` gives each role a free device that matches its `platform` and `device`. Roles
that name a device are served first, then roles that name a platform, then the rest.

Pin roles from the command line (overrides the file):

```sh
tb test chat.yaml --device alice="iPhone 17" --device bob=emulator-5554
```

A suite that mixes single-device and multi-device flows shares one pool of devices:
multi-device flows start first (they need the most devices at once), and single-device flows
fill in as devices come free.

## Reports

A multi-device flow is one test in JUnit and HTML reports. Its device label lists every
role, like `alice=iPhone 17, bob=Pixel 7`. On failure, the debug folder holds a screenshot,
screen listing and hierarchy per role (`screenshot-alice.png`, `screen-bob.txt`, ...).

## Agents

An agent can drive several devices at once without a flow: name them, then address each.

```sh
tb open -d "iPhone 17" --name alice
tb open -d emulator-5554 --name bob
tb do -d alice 'tapOn: Send'
tb wait -d bob "Hi Bob"
```
