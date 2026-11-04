# Working in this repository

## Fixing a GitHub issue

Every fix for an issue filed on this repository comes with a test that fails without the fix
and passes with it.

1. **Write the test first**, from the issue's report, and run it on the code as it is: it
   must fail, and fail for the reason the issue describes. A test that already passes does
   not reproduce the issue: keep looking until one fails.
2. **Then fix**, and run the same test again: it must pass now. Run the tests of the
   packages you touched too.
3. **Where the test goes:**
   - a Go unit test next to the code (`go test ./internal/...`) whenever the bug can be
     reached without a device: most can, through the fake drivers in the tests;
   - when it needs a real device, a step in a bench scenario flow
     (`bench/flows/scenarios`, with the screen it needs in the bench apps and
     `bench/SCENARIOS.md`), or a script in `bench/checks` for what a flow cannot see
     (`tb look`'s listing, for one);
   - name the issue in the test's comment ("issue #12").
4. **Show both runs** in the commit message or the pull request, and in the issue when you
   close it: the failing run on the code without the fix, the passing run with it.
