// capture.js — maintainer-only Playwright capture of the calibration
// page demo flow. Outputs MP4 + stills + DOM snapshots + token dump
// for design handoff. Never shipped to operators.
//
// Strategy: serve the embedded web/* files via a tiny static server,
// open the calibration page in headed Chromium, intercept all /api/v1/*
// fetches with a network failure (which forces the JS into demo mode),
// then capture the resulting deterministic sequence as MP4 + key
// stills + DOM snapshots.
//
// Demo mode is the perfect capture target because:
//   - it walks every phase predictably (always ~30s end-to-end)
//   - it doesn't need a real ventd daemon or HIL hardware
//   - it exercises the SAME HTML/CSS/JS the operator sees on a real run
//
// Usage:
//   cd scripts/dev/capture-calibration
//   npm install
//   npx playwright install chromium
//   npm run capture
//
// Output: ./out/{calibration-flow.mp4, stills/*.png, dom/*.html, tokens/*.css}

const fs   = require('fs');
const path = require('path');
const http = require('http');
const url  = require('url');
const { chromium } = require('playwright');

const REPO_ROOT = path.resolve(__dirname, '../../..');
const WEB_DIR   = path.join(REPO_ROOT, 'web');
const OUT       = path.resolve(__dirname, 'out');

const VIEWPORT = { width: 1440, height: 900 };

// Mime by extension for the static server.
const MIME = {
  '.html':  'text/html; charset=utf-8',
  '.css':   'text/css; charset=utf-8',
  '.js':    'application/javascript; charset=utf-8',
  '.json':  'application/json',
  '.svg':   'image/svg+xml',
  '.png':   'image/png',
  '.jpg':   'image/jpeg',
  '.ico':   'image/x-icon',
  '.woff':  'font/woff',
  '.woff2': 'font/woff2',
};

function serveStatic(req, res) {
  let p = url.parse(req.url).pathname;
  if (p === '/' || p === '/calibration' || p === '/calibration/') p = '/calibration.html';
  // shared/* paths are mounted at /shared/* in production via the
  // server's static handler; web/embed.go uses //go:embed shared/...
  // Same flat directory in dev.
  const file = path.join(WEB_DIR, p);
  if (!file.startsWith(WEB_DIR)) {
    res.writeHead(403); res.end('forbidden'); return;
  }
  fs.stat(file, (err, st) => {
    if (err || !st.isFile()) {
      res.writeHead(404); res.end('404 ' + p); return;
    }
    const ext = path.extname(file).toLowerCase();
    res.writeHead(200, { 'Content-Type': MIME[ext] || 'application/octet-stream', 'Cache-Control': 'no-store' });
    fs.createReadStream(file).pipe(res);
  });
}

function ensureDirs() {
  fs.rmSync(OUT, { recursive: true, force: true });
  for (const d of ['stills', 'dom', 'tokens', 'video']) {
    fs.mkdirSync(path.join(OUT, d), { recursive: true });
  }
}

function copyTokens() {
  const candidates = [
    'shared/tokens.css',
    'shared/brand.css',
    'shared/shell.css',
    'shared/ambient.css',
    'calibration.css',
  ];
  for (const c of candidates) {
    const src = path.join(WEB_DIR, c);
    if (fs.existsSync(src)) {
      const dst = path.join(OUT, 'tokens', path.basename(c));
      fs.copyFileSync(src, dst);
      console.log(`  tokens: ${c}`);
    }
  }
}

// Stage timeline derived empirically from calibration.js's demo
// runner — the demo walks 5 pre-calibrate phases × 2.4s, then ~10
// fans × ~3s of progress, then the finalize curve hero (~7.5s),
// then the done banner. Total ~50-60s.
//
// Stills are taken at *cumulative* times from the page-loaded mark,
// chosen to land on visible states (not transition moments).
const STAGES = [
  { at:  1500, name: '01-page-loaded',           description: 'Initial layout: empty strip list + system card detecting + activity feed waiting' },
  { at:  4500, name: '02-discovery-strips',      description: 'Phase: detecting / installing_driver. Idle pulse view if pipeline removed; system card populating chip name' },
  { at:  9500, name: '03-tach-pairing',          description: 'Phase: detecting_rpm. Strips visible, "pairing tach" status' },
  { at: 14500, name: '04-polarity-probe',        description: 'Phase: probing_polarity. Strips show "polarity probe" status, one active' },
  { at: 19000, name: '05-calibrating-fan-1',     description: 'Phase: calibrating. Strip 1 active with PWM/RPM ticking, others pending' },
  { at: 28000, name: '06-calibrating-mid-run',   description: 'Phase: calibrating. ~3 fans done, current fan progressing, remaining queued' },
  { at: 38000, name: '07-finalize-curve-start',  description: 'Phase: finalizing. Tracer dot at start, FAN STOP band visible, computation panel revealing' },
  { at: 42000, name: '08-finalize-curve-mid',    description: 'Phase: finalizing. Curve drawn through ~50%, slope ticking, anchors appearing' },
  { at: 46000, name: '09-finalize-curve-done',   description: 'Phase: finalizing. Curve fully drawn, all anchors visible, "Curve ready" caption' },
  { at: 50000, name: '10-apply-button-visible',  description: 'Done banner with Apply & Continue button revealed' },
];

(async () => {
  ensureDirs();

  // 1) static server
  const srv = http.createServer(serveStatic);
  await new Promise(r => srv.listen(0, '127.0.0.1', r));
  const port = srv.address().port;
  const baseURL = `http://127.0.0.1:${port}`;
  console.log(`static server: ${baseURL}`);

  // 2) browser
  const browser = await chromium.launch({
    headless: true,
    args: ['--no-sandbox', '--disable-dev-shm-usage'],
  });
  const context = await browser.newContext({
    viewport: VIEWPORT,
    recordVideo: { dir: path.join(OUT, 'video'), size: VIEWPORT },
  });
  const page = await context.newPage();

  // 3) intercept API → force demo mode
  await page.route('**/api/**', route => route.abort('failed'));

  // 4) load calibration page directly
  await page.goto(`${baseURL}/calibration`, { waitUntil: 'domcontentloaded' });

  // 5) capture each stage
  let elapsed = 0;
  for (const stage of STAGES) {
    const wait = Math.max(0, stage.at - elapsed);
    if (wait > 0) await page.waitForTimeout(wait);
    elapsed = stage.at;

    const stillPath = path.join(OUT, 'stills', stage.name + '.png');
    await page.screenshot({ path: stillPath, fullPage: false });

    const domPath = path.join(OUT, 'dom', stage.name + '.html');
    fs.writeFileSync(domPath, await page.content(), 'utf-8');

    console.log(`  ${stage.at.toString().padStart(6)}ms  ${stage.name}`);
  }

  // 6) close → flush video
  await context.close();
  await browser.close();
  srv.close();

  // 7) rename / consolidate the auto-named video file
  const videoDir = path.join(OUT, 'video');
  const videos = fs.readdirSync(videoDir).filter(f => f.endsWith('.webm'));
  if (videos.length > 0) {
    const src = path.join(videoDir, videos[0]);
    const dst = path.join(OUT, 'calibration-flow.webm');
    fs.renameSync(src, dst);
    fs.rmdirSync(videoDir, { recursive: true });
    console.log(`  video: calibration-flow.webm`);
  }

  // 8) copy design tokens for the redesign agent
  copyTokens();

  // 9) write a manifest for design handoff
  const manifest = {
    generatedAt: new Date().toISOString(),
    viewport: VIEWPORT,
    stages: STAGES,
    files: {
      video: 'calibration-flow.webm',
      stills: STAGES.map(s => `stills/${s.name}.png`),
      dom: STAGES.map(s => `dom/${s.name}.html`),
      tokens: ['tokens/tokens.css', 'tokens/brand.css', 'tokens/shell.css', 'tokens/ambient.css', 'tokens/calibration.css'].filter(t => fs.existsSync(path.join(OUT, t))),
    },
    description: 'ventd calibration page capture for claude.ai/design handoff. Demo mode walks every phase deterministically (~50s). Stills land on visible states; DOM snapshots are full HTML at each moment for reasoning about structure.',
  };
  fs.writeFileSync(path.join(OUT, 'MANIFEST.json'), JSON.stringify(manifest, null, 2));

  console.log(`\nDONE — outputs in ${OUT}`);
  process.exit(0);
})().catch(err => {
  console.error('capture failed:', err);
  process.exit(1);
});
