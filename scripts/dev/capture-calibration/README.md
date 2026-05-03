# calibration capture

Maintainer-only Playwright script that records the calibration page demo
flow as an MP4 plus stills + DOM snapshots + design tokens, packaged for
hand-off to claude.ai/design or any external designer.

Never shipped to operators. Lives in `scripts/dev/` because it pulls
~200 MB of npm + chromium that we don't want on production installs.

## Usage

```sh
cd scripts/dev/capture-calibration
npm install              # ~50 MB node_modules
npx playwright install chromium  # ~150 MB headless chromium
npm run capture          # ~50s; outputs to ./out/
```

## Output structure

```
out/
├── MANIFEST.json                    # describes every stage + paths
├── calibration-flow.webm            # full ~50s recording
├── stills/
│   ├── 01-page-loaded.png
│   ├── 02-discovery-strips.png
│   ├── 03-tach-pairing.png
│   ├── 04-polarity-probe.png
│   ├── 05-calibrating-fan-1.png
│   ├── 06-calibrating-mid-run.png
│   ├── 07-finalize-curve-start.png
│   ├── 08-finalize-curve-mid.png
│   ├── 09-finalize-curve-done.png
│   └── 10-apply-button-visible.png
├── dom/                              # full HTML at each stage
│   └── *.html
└── tokens/                           # CSS design system
    ├── tokens.css                    # color + space + type tokens
    ├── brand.css                     # logo + brand-mark
    ├── shell.css                     # page shell layout
    ├── ambient.css                   # background gradient
    └── calibration.css               # calibration-page-specific
```

## Why demo mode

The capture intercepts all `/api/v1/**` fetches and aborts them, which
forces the JS into its built-in demo state. The demo:

- walks every phase in sequence (detecting → installing → scanning → polarity → calibrating → finalizing → done)
- always takes ~50s end-to-end on a 1440×900 viewport
- exercises the same HTML/CSS/JS the operator sees in production
- is fully deterministic — re-running gives byte-identical structure

This means the capture works without HIL hardware, without a running
ventd daemon, and without any test data setup.

## Adjusting timings

Stage timestamps in `capture.js`'s `STAGES` array land on visible
states observed empirically from the demo runner. If `web/calibration.js`
changes the demo cadence, update those `at:` values.

## What to hand off

For a claude.ai/design polish pass, send:

1. The `calibration-flow.webm` (drag into the chat — design supports video)
2. 3-4 of the most representative stills (07/08/09 for the curve;
   05 for active calibration; 10 for the done state)
3. `tokens/tokens.css` (the design system — keeps any redesign on-palette)
4. The `dom/` for the stage you want redesigned (lets the designer
   reason about structure without reverse-engineering)
