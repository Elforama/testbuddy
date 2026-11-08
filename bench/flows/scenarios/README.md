# Scenario flows

The shared Buddy Bench scenario flows ([../../SCENARIOS.md](../../SCENARIOS.md)) run on all
three platforms:

```sh
tb test bench/flows/scenarios -p android
tb test bench/flows/scenarios -p ios
go run ./bench/web &   # serves http://127.0.0.1:7360/
tb test bench/flows/scenarios -e BENCH=http://127.0.0.1:7360/ --headless
```

Share the steps for equivalent UI. Branch only where the user experience differs.
CI selects standalone device capability flows from [../device](../device).
