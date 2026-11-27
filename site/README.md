# Test Buddy website

A buildless GitHub Pages site: HTML, CSS, and a small JavaScript file. No npm
dependencies, analytics, or runtime API. Uses system sans-serif text and monospace code; no external font requests.
All assets and data use relative URLs so the site works at `/testbuddy/`.

## Preview

From the repository root:

```sh
python3 -m http.server 8077 --bind 127.0.0.1 --directory site
```

Open `http://127.0.0.1:8077`. Check desktop and mobile layouts, light and dark themes, the
benchmark charts, the install tabs and the copy buttons.

## Content

The page states what the main [README](../README.md) states, and nothing newer or older:

- the version label (`v0.7.0`, in the hero);
- the benchmark chart, from the README's Speed section (run of 2026-11-26: tb
  0.7.0, Maestro 2.12.1, WebdriverIO 10.2.0). The buttons above the chart switch between
  the Android, iOS and web builds of the demo app; each bar's width is its time as a share
  of the slowest tool's on that app (`--w` on the bar);
- four more charts that the README does not have: tb and Maestro on the sample suites of
  Maestro's own repository (`e2e/workspaces/wikipedia` on Android and iOS,
  `simple_web_view` on iOS, `web` in headless Chrome), run unchanged with
  `--include-tags passing`. Mean (min-max) of 5 timed runs after a warm-up, one at a time,
  on 2026-11-27, tb built from main, Maestro 2.12.1: Wikipedia Android 18.8 s (18.7-18.8)
  against 44.2 s (43.8-44.9); Wikipedia iOS 46.7 s (46.4-47.0) against 73.0 s (71.9-74.0);
  web view app 5.6 s (4.8-6.5) against 17.6 s (17.2-18.1); web samples 13.7 s (13.3-13.9)
  against 131.9 s (131.1-132.5). Every run of both tools passed;
- the install commands, from the README's Install section.

When a release or a new bench round changes the README, change the same numbers and
commands in `index.html`. Other runners are named only where the README names them: Maestro
for flow compatibility, and Maestro and WebdriverIO in the benchmark section.

## Publish

GitHub Pages serves the root of the `gh-pages` branch. Only the files in this directory
belong in the deployment. Keep the source under `site/` on the development branch.

To update a live deployment, clone just the publishing branch into a temporary folder,
copy this directory into it (excluding this README), then review, commit, and push:

```sh
git clone --single-branch --branch gh-pages https://github.com/Elforama/testbuddy.git /tmp/testbuddy-pages-update
rsync -av --exclude README.md site/ /tmp/testbuddy-pages-update/
git -C /tmp/testbuddy-pages-update diff --check
git -C /tmp/testbuddy-pages-update add .
git -C /tmp/testbuddy-pages-update commit -m "Update TestBuddy website"
git -C /tmp/testbuddy-pages-update push origin gh-pages
```

Use a fresh temporary path for each publishing session. GitHub's Pages build deploys the
push. Confirm the Pages action succeeds and check `https://elforama.github.io/testbuddy/`.
The `.nojekyll` file tells Pages to serve the files directly.
