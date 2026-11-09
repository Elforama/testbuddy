# TestBuddy website

A buildless GitHub Pages site: HTML, CSS, and a small JavaScript file. No npm
dependencies, analytics, or runtime API. Uses a single system monospace font stack; no external font requests.
All assets and data use relative URLs so the site works at `/testbuddy/`.

## Preview

From the repository root:

```sh
python3 -m http.server 8077 --bind 127.0.0.1 --directory site
```

Open `http://127.0.0.1:8077`. Check desktop and mobile layouts, all three benchmark
platforms, both timing modes, the plugin disclosure, and the six install targets.

## Evidence

- `data/comparison.json`: nine matched journey observations from the local comparison
  report, using TestBuddy 0.5.1 working tree at base `44485d1` and Other 2.12.1.
  Values retain both JUnit flow durations and complete command durations. Each
  runner's observation is one run, not an average. Three failed journeys are retained
  and excluded from speedup calculations. Retry/watchdog caveats accompany the data.
- `data/validation.json`: the final 0.5.2 local release validation matrix, one round,
  zero whole-flow retries. 53 Android + 54 iOS + 17 web = 124 passing scenarios.
  This is separate from the older comparison and does not claim a population pass rate.
- Original local sources: `build/maestro-comparison/REPORT.md` and
  `build/release-052-scroll/summary.json`. Build directories are not published.

The comparison runner is labeled **Other** in the public site and dataset. The flow
compatibility section names the supported YAML format. CLI and multi-device snippets are usage examples.

When updating evidence, replace the datasets and update the matching static release
validation totals, version labels, and methodology in `index.html`.
Do not mix old comparison timings with a newer runner version label.

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
