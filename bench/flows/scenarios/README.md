# Scenario flows

One flow per Buddy Bench scenario ([../../SCENARIOS.md](../../SCENARIOS.md)), each checking all
of that scenario's edge cases in one run. The same files run on every platform:

```sh
tb test bench/flows/scenarios -p android
tb test bench/flows/scenarios -p ios
go run ./bench/web &   # serves http://127.0.0.1:7360/
tb test bench/flows/scenarios -e BENCH=http://127.0.0.1:7360/ --headless
```

Steps that only exist on one platform are guarded with `when: {platform: ...}`.
