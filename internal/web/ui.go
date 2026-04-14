package web

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Ventd</title>
<style>
/* ── Theme variables ── */
:root {
  --bg:        #0d1117;
  --bg2:       #161b22;
  --bg3:       #21262d;
  --bg4:       #1a1f26;
  --border:    #30363d;
  --border2:   #1a1f26;
  --fg:        #c9d1d9;
  --fg1:       #e6edf3;
  --fg2:       #8b949e;
  --fg3:       #484f58;
  --fg4:       #2a3a4a;
  --teal:      #4fc3a1;
  --teal-h:    #3da88a;
  --teal-bg:   #1a3a2a;
  --teal-bg2:  #2a4a3a;
  --amber:     #e6a23c;
  --amber-bg:  #2a2a1a;
  --red:       #f85149;
  --red-bg:    #3a1a1a;
  --red2:      #f97583;
  --blue:      #58a6ff;
  --blue-bg:   #1a2a3a;
  --purple:    #d2a8ff;
  --cyan:      #79c0ff;
  --btn-pri-fg:#0d1117;
}
[data-theme="light"] {
  --bg:        #f6f8fa;
  --bg2:       #ffffff;
  --bg3:       #f3f4f6;
  --bg4:       #e8ecf0;
  --border:    #d0d7de;
  --border2:   #e8ecf0;
  --fg:        #24292f;
  --fg1:       #1f2328;
  --fg2:       #57606a;
  --fg3:       #8c959f;
  --fg4:       #afb8c1;
  --teal:      #0b8a6e;
  --teal-h:    #096e57;
  --teal-bg:   #d1f5ee;
  --teal-bg2:  #b8e8de;
  --amber:     #7d4e00;
  --amber-bg:  #fff3cd;
  --red:       #cf222e;
  --red-bg:    #ffebe9;
  --red2:      #d73a49;
  --blue:      #0969da;
  --blue-bg:   #ddf4ff;
  --purple:    #8250df;
  --cyan:      #0550ae;
  --btn-pri-fg:#ffffff;
}

*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
body {
  font-family: 'Consolas', 'Monaco', 'Courier New', monospace;
  background: var(--bg); color: var(--fg);
  /* Scale with viewport width: ~14px at 1280px, ~16px at 1440px, capped at 18px */
  font-size: clamp(14px, 1.1vw, 18px);
  transition: background 0.2s, color 0.2s;
}
header {
  display: flex; justify-content: space-between; align-items: center;
  padding: 0.6rem 1.5rem;
  background: var(--bg2); border-bottom: 1px solid var(--border);
}
.header-brand { display: flex; align-items: center; gap: 10px; }
h1 { color: var(--fg1); font-size: 1rem; letter-spacing: 0.05em; }
.header-tagline { color: var(--fg3); font-size: 0.6rem; letter-spacing: 0.12em; text-transform: uppercase; margin-top: 1px; }
/* Theme toggle */
.theme-btn {
  background: none; border: 1px solid var(--border); color: var(--fg2);
  padding: 4px 9px; border-radius: 4px; cursor: pointer; font-size: 1.1rem; line-height: 1;
}
.theme-btn:hover { border-color: var(--teal); color: var(--teal); background: var(--teal-bg); }
/* Live connection indicator */
.live-dot {
  width: 8px; height: 8px; border-radius: 50%;
  background: var(--fg3); flex-shrink: 0; transition: background 0.4s;
}
.live-dot.on { background: var(--teal); animation: pulse-dot 2.5s ease-in-out infinite; }
.live-dot.err { background: var(--red); }
@keyframes pulse-dot {
  0%,100% { box-shadow: 0 0 0 0 rgba(79,195,161,0.5); }
  50%      { box-shadow: 0 0 0 5px rgba(79,195,161,0); }
}
/* Temperature heat colours */
.tc-cool { color: var(--teal); }
.tc-warm { color: var(--amber); }
.tc-hot  { color: var(--red2); }
.tc-crit { color: var(--red); font-weight: bold; }
/* Fan duty colours */
.dc-low  { color: var(--teal); }
.dc-mid  { color: var(--amber); }
.dc-high { color: var(--red); }
.sys-status { color: var(--fg3); font-size: 0.72rem; letter-spacing: 0.02em; margin-right: 0.6rem; }

.layout { display: flex; min-height: calc(100vh - 52px); }

/* Sidebar */
.sidebar {
  width: 260px; min-width: 260px;
  background: var(--bg2); border-right: 1px solid var(--border);
  padding: 10px 0; overflow-y: auto;
  transition: width 0.2s, min-width 0.2s, padding 0.2s, border 0.2s;
}
.sidebar.collapsed { width: 0; min-width: 0; padding: 0; overflow: hidden; border-right: none; }
.sidebar-hdr {
  color: var(--fg2); font-size: 0.65rem; text-transform: uppercase;
  letter-spacing: 0.1em; padding: 8px 12px 4px;
}
.hw-device { border-bottom: 1px solid var(--border2); padding-bottom: 2px; margin-bottom: 2px; }
.hw-device-name {
  color: var(--fg1); font-size: 0.75rem; font-weight: bold;
  padding: 4px 12px; cursor: pointer; display: flex;
  justify-content: space-between; align-items: center;
}
.hw-device-name:hover { background: var(--bg3); }
.hw-device-name .toggle { color: var(--fg3); font-size: 0.65rem; }
.hw-readings { padding: 0 6px 4px 12px; }
.hw-readings.collapsed { display: none; }
.hw-reading {
  display: flex; justify-content: space-between; align-items: center;
  font-size: 0.72rem; padding: 2px 0; color: var(--fg2); gap: 4px;
}
.hw-reading .lbl { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.hw-reading .val { font-variant-numeric: tabular-nums; flex-shrink: 0; }
.hw-reading .val.temp  { color: var(--teal); }
.hw-reading .val.fan   { color: var(--blue); }
.hw-reading .val.volt  { color: var(--amber); }
.hw-reading .val.power { color: var(--red2); }
.hw-reading .val.pct   { color: var(--purple); }
.hw-reading .val.clock { color: var(--cyan); }
.hw-subhdr {
  color: var(--fg3); font-size: 0.58rem; text-transform: uppercase;
  letter-spacing: 0.09em; padding: 5px 0 1px;
  border-top: 1px solid var(--border2); margin-top: 3px;
}
.hw-subhdr:first-child { border-top: none; margin-top: 0; padding-top: 2px; }
.add-sensor-btn {
  background: none; border: 1px solid var(--border); color: var(--fg3);
  padding: 0 4px; border-radius: 3px; cursor: pointer;
  font-size: 0.65rem; line-height: 1.4; flex-shrink: 0;
}
.add-sensor-btn:hover { border-color: var(--teal); color: var(--teal); background: var(--teal-bg); }
.add-sensor-btn.added { border-color: var(--teal-bg2); color: var(--teal-h); cursor: default; }

main { flex: 1; padding: 1rem 1.5rem; max-width: 1100px; overflow-y: auto; }
.section-hdr {
  display: flex; justify-content: space-between; align-items: center;
  margin-bottom: 0.6rem;
}
h2 {
  color: var(--fg2); font-size: 0.7rem; text-transform: uppercase;
  letter-spacing: 0.1em;
}
.section { margin-bottom: 2.5rem; }
.header-actions { display: flex; align-items: center; gap: 10px; }
.badge {
  background: var(--amber); color: #111;
  padding: 2px 8px; border-radius: 3px;
  font-size: 0.7rem; font-weight: bold;
}
button {
  font-family: inherit; font-size: 0.75rem;
  padding: 4px 10px; border: 1px solid var(--border);
  background: var(--bg3); color: var(--fg); border-radius: 4px; cursor: pointer;
}
button:hover { background: var(--border); }
button.primary {
  background: var(--teal); color: var(--btn-pri-fg);
  border-color: var(--teal); font-weight: bold;
}
button.primary:hover { background: var(--teal-h); }
button.primary:disabled { opacity: 0.4; cursor: not-allowed; }
button.danger { border-color: var(--red); color: var(--red); }
button.danger:hover { background: var(--red); color: #fff; }
.add-btns { display: flex; gap: 4px; }

.notification {
  position: fixed; top: 12px; right: 12px;
  padding: 8px 14px; border-radius: 4px;
  font-size: 0.8rem; z-index: 100; max-width: 400px;
}
.notification.ok { background: var(--teal-bg); color: var(--teal); border: 1px solid var(--teal); }
.notification.error { background: var(--red-bg); color: var(--red); border: 1px solid var(--red); }

/* Cards */
.card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 16px;
}
.card {
  background: var(--bg2); border: 1px solid var(--border);
  border-radius: 8px; padding: 14px 16px;
}
.card-name {
  color: var(--fg1); font-size: 0.85rem; font-weight: bold;
  margin-bottom: 4px;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.card-name-edit {
  display: flex; align-items: center; gap: 4px; margin-bottom: 4px;
}
.card-name-edit input {
  background: transparent; border: none; border-bottom: 1px solid transparent;
  color: var(--fg1); font-size: 0.85rem; font-weight: bold;
  font-family: inherit; width: 100%; padding: 0;
}
.card-name-edit input:hover { border-bottom-color: var(--border); }
.card-name-edit input:focus { outline: none; border-bottom-color: var(--teal); }
.card-name-edit .edit-icon { color: var(--fg3); font-size: 0.7rem; cursor: pointer; flex-shrink: 0; }

/* Sensor cards */
.sensor-path { color: var(--fg3); font-size: 0.68rem; margin-bottom: 2px;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  display: none; }
.card:hover .sensor-path { display: block; }
.sensor-val { color: var(--teal); font-size: 0.85rem; margin-bottom: 6px;
  font-variant-numeric: tabular-nums; }
.sensor-actions { display: flex; justify-content: flex-end; }

/* Fan cards */
.fan-meta { display: flex; justify-content: space-between; align-items: center; margin-bottom: 4px; }
.fan-rpm { color: var(--fg2); font-size: 0.8rem; }
.fan-duty {
  display: flex; align-items: center; gap: 6px;
  font-size: 0.8rem; margin-bottom: 6px;
  font-variant-numeric: tabular-nums;
}
.duty-bar { flex: 1; height: 4px; background: var(--bg3); border-radius: 2px; overflow: hidden; }
.duty-bar .fill { height: 100%; border-radius: 2px; transition: width 0.3s, background 0.3s; }
.card select {
  width: 100%; padding: 3px 6px;
  background: var(--bg); border: 1px solid var(--border);
  color: var(--fg); border-radius: 3px;
  font-family: inherit; font-size: 0.75rem;
}
/* Mode toggle */
.mode-toggle { display: flex; gap: 0; margin-bottom: 6px; border-radius: 4px; overflow: hidden; border: 1px solid var(--border); }
.mode-btn { flex: 1; padding: 3px 6px; border: none; border-radius: 0; font-size: 0.7rem; background: var(--bg3); color: var(--fg2); cursor: pointer; }
.mode-btn:hover { background: var(--border); color: var(--fg); }
.mode-btn.active { background: var(--teal); color: var(--btn-pri-fg); font-weight: bold; }
/* Manual slider */
.manual-slider { display: flex; align-items: center; gap: 6px; margin-bottom: 4px; }
.manual-slider input[type=range] { flex: 1; }
.manual-pct { color: var(--blue); font-size: 0.8rem; min-width: 32px; font-variant-numeric: tabular-nums; }
/* Calibration */
.cal-btn { width: 100%; margin-top: 4px; font-size: 0.7rem; padding: 3px 6px; }
.cal-running { color: var(--amber); font-size: 0.72rem; margin-top: 4px; }
.cal-prog-bar { height: 3px; background: var(--bg3); border-radius: 2px; overflow: hidden; margin-top: 3px; }
.cal-prog-bar .fill { height: 100%; background: var(--amber); border-radius: 2px; transition: width 0.3s; }
.cal-result { color: var(--fg3); font-size: 0.68rem; margin-top: 4px; border-top: 1px solid var(--border2); padding-top: 4px; display: none; }
.card:hover .cal-result { display: block; }

/* Setup wizard overlay */
#setup-overlay {
  position: fixed; inset: 0; background: var(--bg);
  z-index: 200; display: flex; align-items: flex-start;
  justify-content: center; padding: 2rem 1rem; overflow-y: auto;
}
#setup-overlay.hidden { display: none; }
.setup-box {
  width: 100%; max-width: 700px;
  background: var(--bg2); border: 1px solid var(--border);
  border-radius: 10px; padding: 2rem;
}
.setup-box h2 {
  color: var(--fg1); font-size: 1.1rem; margin-bottom: 0.3rem;
  text-transform: none; letter-spacing: 0;
}
.setup-box .sub { color: var(--fg2); font-size: 0.82rem; margin-bottom: 1.5rem; }
.setup-intro { margin-bottom: 1.5rem; }
.setup-intro p { color: var(--fg); font-size: 0.85rem; line-height: 1.6; margin-bottom: 0.5rem; }
.setup-start-area { display: flex; gap: 10px; align-items: center; margin-bottom: 1.5rem; }
#btn-setup-start { font-size: 0.85rem; padding: 7px 18px; }
.setup-fan-table { width: 100%; border-collapse: collapse; margin-bottom: 1.5rem; font-size: 0.8rem; }
.setup-fan-table th {
  text-align: left; color: var(--fg2); font-size: 0.65rem;
  text-transform: uppercase; letter-spacing: 0.08em;
  padding: 4px 8px; border-bottom: 1px solid var(--border);
}
.setup-fan-table td { padding: 6px 8px; border-bottom: 1px solid var(--border2); vertical-align: middle; }
.setup-fan-table tr:last-child td { border-bottom: none; }
.phase-badge {
  display: inline-block; padding: 2px 7px; border-radius: 3px;
  font-size: 0.65rem; font-weight: bold; text-transform: uppercase; letter-spacing: 0.06em;
}
.phase-pending    { background: var(--bg4);    color: var(--fg3); }
.phase-detecting  { background: var(--blue-bg); color: var(--blue); }
.phase-calibrating{ background: var(--amber-bg);color: var(--amber); }
.phase-done       { background: var(--teal-bg); color: var(--teal); }
.phase-found      { background: var(--teal-bg); color: var(--teal); }
.phase-none       { background: var(--bg4);    color: var(--fg3); }
.phase-na         { background: var(--bg4);    color: var(--fg4); }
.phase-skipped    { background: var(--bg4);    color: var(--fg3); }
.phase-error      { background: var(--red-bg); color: var(--red); }
.setup-prog-bar { height: 3px; background: var(--bg4); border-radius: 2px; margin-top: 4px; overflow: hidden; }
.setup-prog-bar .fill { height: 100%; background: var(--amber); border-radius: 2px; transition: width 0.3s; }
.setup-summary {
  background: var(--bg); border: 1px solid var(--border); border-radius: 6px;
  padding: 1rem; margin-bottom: 1.5rem; font-size: 0.82rem;
}
.setup-summary h3 { color: var(--fg1); font-size: 0.85rem; margin-bottom: 0.6rem; margin-top: 1rem; }
.setup-summary h3:first-child { margin-top: 0; }
.setup-summary ul { list-style: none; }
.setup-summary li { color: var(--fg2); padding: 2px 0; font-size: 0.8rem; }
.setup-summary li span { color: var(--teal); }
.hw-profile { margin-bottom: 0.8rem; padding-bottom: 0.8rem; border-bottom: 1px solid var(--bg3); }
.hw-profile-row { color: var(--fg2); font-size: 0.8rem; padding: 2px 0; }
.hw-profile-row strong { color: var(--fg); }
.hw-profile-row .val { color: var(--teal); }
.curve-notes { margin-bottom: 0.8rem; }
.curve-notes ul { list-style: none; margin: 0.3rem 0 0 0; }
.curve-notes li { color: var(--fg2); font-size: 0.78rem; padding: 1px 0; font-family: monospace; }
.setup-actions { display: flex; gap: 10px; align-items: center; }
#btn-setup-apply { font-size: 0.85rem; padding: 7px 18px; }
.setup-error { color: var(--red); font-size: 0.82rem; margin-bottom: 1rem; }
.setup-nofans { margin-bottom: 1.5rem; }
.setup-nofans-box {
  background: var(--bg); border: 1px solid var(--border);
  border-radius: 6px; padding: 1rem; margin-bottom: 1rem; font-size: 0.82rem;
}
.setup-nofans-box h3 { color: var(--fg1); font-size: 0.85rem; margin: 0 0 0.6rem; }
.setup-nofans-board { color: var(--fg2); margin-bottom: 0.5rem; }
.setup-nofans-board strong { color: var(--teal); }
.setup-nofans-chips { color: var(--fg3); font-size: 0.78rem; margin-bottom: 0.3rem; }
.setup-nofans-explain { color: var(--fg2); font-size: 0.82rem; line-height: 1.6; margin: 0.7rem 0; }
.setup-nofans-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 0.8rem; }
.fix-log {
  background: var(--bg3); border: 1px solid var(--border); border-radius: 5px;
  padding: 0.6rem 0.8rem; margin-top: 0.8rem; font-size: 0.75rem; font-family: monospace;
  max-height: 200px; overflow-y: auto; color: var(--fg2); display: none;
}
.fix-log-line { margin: 1px 0; white-space: pre-wrap; word-break: break-all; }

/* Curve cards */
.curve-card { cursor: pointer; transition: border-color 0.15s; }
.curve-card:hover { border-color: var(--teal); }
.curve-card.active { border-color: var(--teal); box-shadow: 0 0 0 1px var(--teal); }
.card-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 4px; }
.type-badge { font-size: 0.6rem; padding: 1px 5px; border-radius: 3px; letter-spacing: 0.04em; font-weight: bold; }
.type-linear { background: var(--teal-bg);  color: var(--teal); }
.type-fixed  { background: var(--blue-bg);  color: var(--blue); }
.type-mix    { background: var(--amber-bg); color: var(--amber); }
.mini-graph { display: block; width: 100%; height: 36px; margin: 2px 0; }
.card-output { font-size: 0.75rem; color: var(--fg2); }

/* Editor */
.editor { background: var(--bg2); border: 1px solid var(--border); border-radius: 8px; padding: 14px; margin-top: 10px; }
.editor-svg { max-width: 520px; margin-bottom: 10px; }
.editor-svg svg { width: 100%; display: block; }
.editor-form { display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 8px; margin-bottom: 10px; }
.fg { display: flex; flex-direction: column; gap: 2px; }
.fg.wide { grid-column: 1 / -1; }
.fg label { font-size: 0.65rem; color: var(--fg2); text-transform: uppercase; }
.fg input, .fg select {
  padding: 4px 6px; background: var(--bg); border: 1px solid var(--border);
  color: var(--fg); border-radius: 3px; font-family: inherit; font-size: 0.8rem;
}
.fg input:focus, .fg select:focus { outline: none; border-color: var(--teal); }
.fg input[type=range] { padding: 0; }
.editor-actions { display: flex; gap: 8px; justify-content: flex-end; }
.source-list label {
  display: flex; align-items: center; gap: 5px;
  font-size: 0.8rem; color: var(--fg); cursor: pointer;
  text-transform: none; margin-bottom: 2px;
}
.fixed-slider { display: flex; align-items: center; gap: 8px; }
.fixed-slider input[type=range] { flex: 1; }
.fixed-slider .pct { color: var(--blue); font-size: 0.8rem; min-width: 35px; }
</style>
</head>
<body>

<div id="notification" hidden></div>

<!-- Setup wizard overlay — shown on first boot when no config exists -->
<div id="setup-overlay" class="hidden">
  <div class="setup-box">
    <div style="display:flex;justify-content:space-between;align-items:flex-start;margin-bottom:0.3rem">
      <h2 style="margin:0">Ventd Setup</h2>
      <button class="theme-btn" onclick="toggleTheme()" title="Toggle light/dark mode" style="margin-top:2px">&#9790;</button>
    </div>
    <p class="sub">Detecting your hardware and setting up fan control automatically.</p>

    <!-- Phase status shown while wizard is running -->
    <div id="setup-phase-area" style="display:none">
      <div id="setup-phase-status" style="color:var(--fg2);font-size:0.85rem;margin-bottom:0.8rem;min-height:1.2em"></div>
      <div id="setup-board-line" style="display:none;color:var(--teal);font-size:0.82rem;margin-bottom:0.4rem;font-weight:600"></div>
      <div id="setup-chip-line" style="display:none;color:var(--amber);font-size:0.82rem;margin-bottom:0.8rem"></div>
      <!-- Install log — visible during installing_driver phase -->
      <div id="setup-install-log" style="display:none;background:var(--bg3);border:1px solid var(--border);border-radius:6px;padding:10px 12px;font-size:0.72rem;max-height:160px;overflow-y:auto;margin-bottom:1rem;color:var(--fg2);font-family:monospace"></div>
    </div>

    <div id="setup-progress-area" style="display:none">
      <table class="setup-fan-table" id="setup-fan-table">
        <thead><tr>
          <th>Fan</th><th>Type</th><th>Detection</th><th>Calibration</th><th>Result</th>
        </tr></thead>
        <tbody id="setup-fan-tbody"></tbody>
      </table>
      <div id="setup-error" class="setup-error" style="display:none"></div>
    </div>

    <div id="setup-reboot-panel" style="display:none;background:var(--amber-bg,#2d2400);border:1px solid var(--amber,#c89020);border-radius:8px;padding:1.2rem 1.4rem;margin-top:1rem">
      <div style="font-weight:600;margin-bottom:0.5rem;color:var(--amber,#c89020)">Reboot Required</div>
      <div id="setup-reboot-msg" style="font-size:0.85rem;margin-bottom:1rem;line-height:1.5"></div>
      <button class="primary" onclick="doReboot()" id="btn-reboot">Reboot Now</button>
      <span id="reboot-status" style="margin-left:1rem;font-size:0.85rem;color:var(--fg2)"></span>
    </div>

    <div id="setup-done-area" style="display:none">
      <div class="setup-summary" id="setup-summary"></div>
      <div class="setup-actions" id="setup-actions">
        <button id="btn-setup-apply" class="primary" onclick="setupApply()">Apply Configuration</button>
        <span id="setup-apply-hint" style="color:var(--fg2);font-size:0.8rem"></span>
      </div>
      <div style="display:none;color:var(--amber);font-size:0.85rem;margin-top:1rem" id="setup-restarting">
        Restarting daemon… <span id="setup-restart-dots">.</span>
      </div>
    </div>
  </div>
</div>

<header>
  <div class="header-brand">
    <svg width="28" height="28" viewBox="0 0 24 24" fill="var(--teal)" xmlns="http://www.w3.org/2000/svg" style="flex-shrink:0">
      <path d="M12 12 Q8 9 9 4 Q13 3 13 8z" opacity="0.85"/>
      <path d="M12 12 Q15 8 20 9 Q21 13 16 13z" opacity="0.85"/>
      <path d="M12 12 Q16 15 15 20 Q11 21 11 16z" opacity="0.85"/>
      <path d="M12 12 Q9 16 4 15 Q3 11 8 11z" opacity="0.85"/>
      <circle cx="12" cy="12" r="2"/>
    </svg>
    <div>
      <h1>Ventd</h1>
      <div class="header-tagline">System Fan Controller</div>
    </div>
  </div>
  <div class="header-actions">
    <span id="sys-status" class="sys-status"></span>
    <span id="live-dot" class="live-dot" title="Live data"></span>
    <span id="dirty" class="badge" hidden>Unsaved</span>
    <button id="btn-apply" class="primary" disabled>Apply</button>
    <button id="btn-sidebar" onclick="toggleSidebar()" title="Toggle hardware panel" style="padding:4px 8px">&#9776;</button>
    <button id="btn-theme" class="theme-btn" onclick="toggleTheme()" title="Toggle light/dark mode">&#9790;</button>
    <button id="btn-settings" onclick="openSettings()" title="Settings" style="padding:4px 8px">&#9881;</button>
  </div>
</header>

<!-- Settings modal -->
<div id="settings-overlay" style="display:none;position:fixed;inset:0;background:rgba(0,0,0,0.6);z-index:300;align-items:center;justify-content:center">
  <div style="background:var(--bg2);border:1px solid var(--border);border-radius:10px;padding:1.5rem;width:100%;max-width:420px">
    <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:1.2rem">
      <h2 style="color:var(--fg1);font-size:0.95rem;text-transform:none;letter-spacing:0">Settings</h2>
      <button onclick="closeSettings()" style="background:none;border:none;color:var(--fg2);font-size:1.1rem;cursor:pointer;padding:0">&#10005;</button>
    </div>
    <div style="border-top:1px solid var(--border);padding-top:1rem">
      <div style="display:flex;justify-content:space-between;align-items:center;padding:0.6rem 0">
        <div>
          <div style="color:var(--fg1);font-size:0.85rem">Reset to Initial Setup</div>
          <div style="color:var(--fg2);font-size:0.75rem;margin-top:2px">Remove current config and re-run the setup wizard</div>
        </div>
        <button class="danger" onclick="confirmReset()" style="flex-shrink:0;margin-left:1rem">Reset</button>
      </div>
    </div>
    <div id="settings-status" style="margin-top:0.8rem;font-size:0.8rem;color:var(--fg2)"></div>
  </div>
</div>
<div class="layout">
  <div class="sidebar" id="sidebar">
    <div class="sidebar-hdr">Hardware Monitor</div>
    <div id="hw-devices" style="color:var(--fg3);font-size:0.75rem;padding:8px 12px">Loading...</div>
  </div>
  <main>
    <div class="section">
      <div class="section-hdr"><h2>Sensors</h2></div>
      <div class="card-grid" id="sensor-cards"></div>
    </div>
    <div class="section">
      <div class="section-hdr"><h2>Controls</h2></div>
      <div class="card-grid" id="fan-cards"></div>
    </div>
    <div class="section">
      <div class="section-hdr">
        <h2>Curves</h2>
        <div class="add-btns">
          <button onclick="addCurve('linear')">+ Linear</button>
          <button onclick="addCurve('fixed')">+ Fixed</button>
          <button onclick="addCurve('mix')">+ Mix</button>
          <button onclick="autoCurve()">Auto</button>
        </div>
      </div>
      <div class="card-grid" id="curve-cards"></div>
    </div>
    <div id="curve-editor"></div>
  </main>
</div>

<script>
"use strict";

let cfg = null, sts = null, hw = null, selIdx = -1, dirty = false, dragging = null;
let hwCollapsed = {}, calStatuses = {}, calResults = {};

const G = {l:40, r:490, t:15, b:230};
G.w = G.r-G.l; G.h = G.b-G.t;
// x-axis is sensor value (0-100 for temp/pct, 0-1000 for others — scaled to 0-100 display)
function v2x(v){ return G.l+(Math.min(v,100)/100)*G.w; }
function p2y(p){ return G.b-(p/255)*G.h; }
function x2v(x){ return Math.round(Math.max(0,Math.min(100,(x-G.l)/G.w*100))); }
function y2p(y){ let pct=Math.round(Math.max(0,Math.min(100,(G.b-y)/G.h*100))); return Math.round(pct/100*255); }
function esc(s){ return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;'); }
function p2pct(p){ return isNaN(p) ? 0 : Math.round(p/255*100); }
function pct2p(pct){ return isNaN(pct) ? 0 : Math.round(pct/100*255); }

// tempClass returns a CSS class for heat-colouring a temperature value.
function tempClass(val){
  if(val < 60) return 'tc-cool';
  if(val < 75) return 'tc-warm';
  if(val < 90) return 'tc-hot';
  return 'tc-crit';
}

// dutyColor returns a CSS variable reference for a fan duty % (0-100).
function dutyColor(pct){
  if(pct < 50) return 'var(--teal)';
  if(pct < 75) return 'var(--amber)';
  return 'var(--red)';
}

function sensorUnit(s){
  if(!s) return '';
  if(s.type==='nvidia'){
    const m={temp:'°C',util:'%',mem_util:'%',power:'W',clock_gpu:'MHz',clock_mem:'MHz',fan_pct:'%'};
    return m[s.metric||'temp']||'';
  }
  const b=(s.path||'').split('/').pop();
  if(b.startsWith('temp')) return '°C';
  if(b.startsWith('in')) return 'V';
  if(b.startsWith('power')) return 'W';
  if(b.startsWith('fan')) return 'RPM';
  return '';
}

function fmtSensorVal(val, unit){
  if(unit==='°C') return val.toFixed(1)+'°C';
  if(unit==='V') return val.toFixed(3)+' V';
  if(unit==='W') return val.toFixed(1)+' W';
  if(unit==='RPM') return Math.round(val)+' RPM';
  if(unit==='MHz') return Math.round(val)+' MHz';
  if(unit==='%') return Math.round(val)+'%';
  return val.toFixed(1)+(unit?' '+unit:'');
}

// ── API ──

async function loadConfig(){
  try {
    const r = await fetch('/api/config');
    cfg = await r.json();
    if(selIdx<0 && cfg.curves && cfg.curves.length>0) selIdx=0;
    render();
  } catch(e){ notify('Load config: '+e.message,'error'); }
}

async function loadStatus(){
  const dot = document.getElementById('live-dot');
  try {
    const r = await fetch('/api/status');
    sts = await r.json();
    if(dot){ dot.className='live-dot on'; clearTimeout(dot._t); dot._t=setTimeout(()=>{ dot.className='live-dot'; },2000); }
    const editingFan = document.activeElement && document.activeElement.closest('.card-name-edit');
    const editingSensor = document.activeElement && document.activeElement.closest('.sensor-name-edit');
    const fanSelectOpen = document.activeElement && document.activeElement.tagName === 'SELECT'
                          && document.activeElement.closest('#fan-cards');
    const anyCalRunning = Object.values(calStatuses).some(s=>s.running);
    renderSensorBar();
    if(!editingFan && !anyCalRunning && !fanSelectOpen) renderFanCards();
    if(!editingSensor) renderSensorCards();
    renderCurveCards();
    if(!dragging && selIdx>=0 && cfg && cfg.curves[selIdx] && cfg.curves[selIdx].type==='linear'){
      drawSVG(cfg.curves[selIdx]);
    }
  } catch(e){ if(dot) dot.className='live-dot err'; }
}

async function loadHardware(){
  try {
    const r = await fetch('/api/hardware');
    hw = await r.json();
    renderHardware();
  } catch(e){}
}

async function loadCalibration(){
  try {
    const [sr, rr] = await Promise.all([
      fetch('/api/calibrate/status').then(r=>r.json()),
      fetch('/api/calibrate/results').then(r=>r.json()),
    ]);
    calStatuses = {};
    (sr||[]).forEach(s => { calStatuses[s.pwm_path] = s; });
    calResults = rr || {};
    renderFanCards();
  } catch(e){}
}

async function applyConfig(){
  try {
    const r = await fetch('/api/config',{
      method:'PUT', headers:{'Content-Type':'application/json'},
      body:JSON.stringify(cfg)
    });
    if(r.ok){
      dirty=false;
      document.getElementById('dirty').hidden=true;
      document.getElementById('btn-apply').disabled=true;
      notify('Configuration applied','ok');
      loadConfig();
    } else { notify(await r.text(),'error'); }
  } catch(e){ notify('Apply failed: '+e.message,'error'); }
}

function markDirty(){
  dirty=true;
  document.getElementById('dirty').hidden=false;
  document.getElementById('btn-apply').disabled=false;
}
function notify(msg,type){
  const el=document.getElementById('notification');
  el.textContent=msg; el.className='notification '+type; el.hidden=false;
  clearTimeout(el._t); el._t=setTimeout(()=>el.hidden=true,5000);
}

// ── Render ──

function render(){
  renderSensorBar(); renderSensorCards(); renderFanCards(); renderCurveCards(); renderEditor(); renderHardware();
}

function renderSensorBar(){
  if(!sts) return;
  const parts = sts.sensors.map(s => {
    const cls = s.unit==='°C' ? tempClass(s.value) : '';
    const val = '<span'+(cls?' style="color:var(--teal)"':'')+'>'+fmtSensorVal(s.value, s.unit)+'</span>';
    return '<span style="color:var(--fg2)">'+esc(s.name)+'</span> '+val;
  });
  const dot = '<span style="color:var(--fg4)"> · </span>';
  document.getElementById('sys-status').innerHTML = parts.join(dot);
}

function renderHardware(){
  if(!hw || !hw.length){
    document.getElementById('hw-devices').innerHTML='<span style="color:var(--fg3);font-size:0.75rem">No devices found</span>';
    return;
  }
  // Build set of already-added sensor paths+metrics for dedup
  const added = new Set();
  if(cfg && cfg.sensors){
    cfg.sensors.forEach(s => added.add(s.type+'|'+(s.path||'')+'|'+(s.metric||'')));
  }

  const valClass = r => {
    if(r.unit==='°C') return 'temp';
    if(r.unit==='RPM') return 'fan';
    if(r.unit==='V') return 'volt';
    if(r.unit==='W') return 'power';
    if(r.unit==='%') return 'pct';
    if(r.unit==='MHz') return 'clock';
    return '';
  };
  const fmtVal = r => {
    if(r.unit==='°C') return r.value.toFixed(1)+'°C';
    if(r.unit==='RPM') return Math.round(r.value)+' RPM';
    if(r.unit==='V') return r.value.toFixed(3)+' V';
    if(r.unit==='W') return r.value.toFixed(1)+' W';
    if(r.unit==='%') return Math.round(r.value)+'%';
    if(r.unit==='MHz') return Math.round(r.value)+' MHz';
    return r.value+' '+r.unit;
  };
  const renderReading = r => {
    const akey = r.sensor_type+'|'+r.sensor_path+'|'+(r.metric||'');
    const isAdded = added.has(akey);
    const btnData = 'data-st="'+esc(r.sensor_type)+'" data-sp="'+esc(r.sensor_path)+'" '+
      'data-mt="'+esc(r.metric||'')+'" data-lbl="'+esc(r.label)+'"';
    const btn = '<button class="add-sensor-btn'+(isAdded?' added':'')+'" '+
      (isAdded?'disabled ':'')+btnData+
      (isAdded?'' : ' onclick="addSensorFromReading(this)"')+
      '>'+(isAdded?'\u2713':'+')+
      '</button>';
    const vc = r.unit==='°C' ? tempClass(r.value) : valClass(r);
    return '<div class="hw-reading">'+
      '<span class="lbl">'+esc(r.label)+'</span>'+
      '<span class="val '+vc+'">'+fmtVal(r)+'</span>'+
      btn+
    '</div>';
  };

  // Reading groups shown in display order; only non-empty groups get a subheading.
  const readingGroups = [
    { label: 'Temperatures', test: r => r.unit === '\u00b0C' },
    { label: 'Fan Speeds',   test: r => r.unit === 'RPM' },
    { label: 'Voltages',     test: r => r.unit === 'V' },
    { label: 'Power',        test: r => r.unit === 'W' },
    { label: 'Utilization',  test: r => r.unit === '%' },
    { label: 'Clocks',       test: r => r.unit === 'MHz' },
  ];

  document.getElementById('hw-devices').innerHTML = hw.map(dev => {
    const key = dev.path;
    const collapsed = hwCollapsed[key];

    let readingsHtml = '';
    const needSubhdrs = readingGroups.filter(g => dev.readings.some(g.test)).length > 1;
    readingGroups.forEach(grp => {
      const group = dev.readings.filter(grp.test);
      if(!group.length) return;
      if(needSubhdrs){
        readingsHtml += '<div class="hw-subhdr">'+grp.label+'</div>';
      }
      readingsHtml += group.map(renderReading).join('');
    });

    return '<div class="hw-device">'+
      '<div class="hw-device-name" onclick="toggleHw(\''+esc(key)+'\')">'+
        '<span>'+esc(dev.name)+'</span>'+
        '<span class="toggle">'+(collapsed?'\u25b6':'\u25bc')+'</span>'+
      '</div>'+
      '<div class="hw-readings'+(collapsed?' collapsed':'')+'">'+readingsHtml+'</div>'+
    '</div>';
  }).join('');
}

function toggleHw(key){
  hwCollapsed[key]=!hwCollapsed[key];
  renderHardware();
}

function addSensorFromReading(btn){
  if(!cfg) return;
  const st = btn.dataset.st;
  const sp = btn.dataset.sp;
  const mt = btn.dataset.mt;
  const lbl = btn.dataset.lbl;
  // Generate unique name
  let nm = lbl.toLowerCase().replace(/[^a-z0-9]+/g,'_').replace(/^_|_$/g,'');
  if(!nm) nm = 'sensor';
  let base = nm, n = 1;
  while(cfg.sensors.some(s=>s.name===nm)) nm = base+'_'+n++;
  const s = {name:nm, type:st, path:sp};
  if(mt) s.metric = mt;
  cfg.sensors.push(s);
  markDirty(); renderSensorCards(); renderHardware();
  notify('Added sensor "'+nm+'" — rename it in the Sensors section','ok');
}

function renderSensorCards(){
  if(!cfg) return;
  document.getElementById('sensor-cards').innerHTML = cfg.sensors.map((s,i) => {
    const st = sts ? sts.sensors.find(x=>x.name===s.name) : null;
    const unit = st ? st.unit : sensorUnit(s);
    const val = st ? fmtSensorVal(st.value, st.unit) : '—';
    const valCls = (st && st.unit==='°C') ? 'sensor-val '+tempClass(st.value) : 'sensor-val';
    const pathDisplay = s.type==='nvidia'
      ? 'GPU '+(s.path||'0')+(s.metric?' · '+s.metric:'')
      : (s.path||'').replace('/sys/class/hwmon/','');
    return '<div class="card">'+
      '<div class="card-name-edit sensor-name-edit">'+
        '<input type="text" value="'+esc(s.name)+'" '+
          'data-orig="'+esc(s.name)+'" '+
          'onblur="renameSensor('+i+',this)" '+
          'onkeydown="if(event.key===\'Enter\'){this.blur();}" '+
          'title="Click to rename">'+
        '<span class="edit-icon">\u270e</span>'+
      '</div>'+
      '<div class="sensor-path">'+esc(pathDisplay)+'</div>'+
      '<div class="'+valCls+'">'+val+'</div>'+
      '<div class="sensor-actions">'+
        '<button class="danger" onclick="deleteSensor('+i+')" title="Remove sensor">&#x2715;</button>'+
      '</div>'+
    '</div>';
  }).join('');
}

function renderFanCards(){
  if(!cfg) return;
  document.getElementById('fan-cards').innerHTML = cfg.controls.map((ctrl,i) => {
    const st = sts ? sts.fans.find(f=>f.name===ctrl.fan) : null;
    const rpm = st && st.rpm!=null ? st.rpm+' RPM' : '\u2014';
    const duty = st ? Math.round(st.duty_pct) : 0;
    const isManual = ctrl.manual_pwm != null;
    const manualPct = isManual ? Math.round(ctrl.manual_pwm/255*100) : 50;

    // find fan config for pwm_path (needed for calibrate)
    const fanCfg = cfg.fans ? cfg.fans.find(f=>f.name===ctrl.fan) : null;
    const pwmPath = fanCfg ? fanCfg.pwm_path : '';

    // calibration state
    const calSt = pwmPath ? calStatuses[pwmPath] : null;
    const calRes = pwmPath ? calResults[pwmPath] : null;
    const isCalibrating = calSt && calSt.running;

    let controlRow = '';
    if(isManual){
      controlRow = '<div class="manual-slider">'+
        '<input type="range" min="0" max="100" step="1" value="'+manualPct+'" '+
          'oninput="updManualPWM('+i+',+this.value,this)">'+
        '<span class="manual-pct">'+manualPct+'%</span>'+
      '</div>';
    } else {
      const opts = cfg.curves.map(c =>
        '<option value="'+esc(c.name)+'"'+(c.name===ctrl.curve?' selected':'')+'>'+esc(c.name)+'</option>'
      ).join('');
      controlRow = '<select onchange="updCtrl('+i+',this.value)">'+opts+'</select>';
    }

    let calSection = '';
    if(isCalibrating){
      calSection = '<div class="cal-running">\u23f3 Calibrating\u2026 PWM '+calSt.current_pwm+'</div>'+
        '<div class="cal-prog-bar"><div class="fill" style="width:'+calSt.progress+'%"></div></div>';
    } else {
      const calBtnDisabled = isCalibrating || !pwmPath;
      calSection = '<button class="cal-btn" '+(calBtnDisabled?'disabled ':'')+
        'onclick="startCalibration(\''+esc(pwmPath)+'\')" title="Measure start PWM and max RPM">\u25b6 Calibrate</button>';
    }
    let calResultRow = '';
    if(calRes && !isCalibrating){
      const sp = calRes.start_pwm ? Math.round(calRes.start_pwm/255*100)+'%' : '\u2014';
      const isNvidiaFan = fanCfg && fanCfg.type === 'nvidia';
      const mr = calRes.max_rpm
        ? (isNvidiaFan ? Math.round(calRes.max_rpm/255*100)+'%' : calRes.max_rpm+' RPM')
        : '\u2014';
      calResultRow = '<div class="cal-result">Start: '+sp+' \u00b7 Max: '+mr+'</div>';
    }

    const hasRPM = fanCfg && (fanCfg.rpm_path || fanCfg.type !== 'nvidia');
    const detectDisabled = !pwmPath || (fanCfg && fanCfg.type==='nvidia') || isCalibrating;
    const detectBtn = '<button style="font-size:0.65rem;padding:1px 5px" '+
      (detectDisabled?'disabled ':'')+
      'onclick="detectRPM(\''+esc(pwmPath)+'\','+i+',this)" title="Auto-detect RPM sensor">\u{1F50D}</button>';

    return '<div class="card">'+
      '<div class="card-name-edit">'+
        '<input type="text" value="'+esc(ctrl.fan)+'" '+
          'data-orig="'+esc(ctrl.fan)+'" '+
          'onblur="renameFan('+i+',this)" '+
          'onkeydown="if(event.key===\'Enter\'){this.blur();}" '+
          'title="Click to rename">'+
        '<span class="edit-icon">\u270e</span>'+
      '</div>'+
      '<div class="fan-meta">'+
        '<div class="fan-rpm">'+rpm+'</div>'+
        detectBtn+
      '</div>'+
      '<div class="fan-duty">'+
        '<div class="duty-bar"><div class="fill" style="width:'+duty+'%;background:'+dutyColor(duty)+'"></div></div>'+
        '<span style="color:'+dutyColor(duty)+'">'+duty+'%</span>'+
      '</div>'+
      '<div class="mode-toggle">'+
        '<button class="mode-btn'+(isManual?'':' active')+'" onclick="setManualMode('+i+',false)">Curve</button>'+
        '<button class="mode-btn'+(isManual?' active':'')+'" onclick="setManualMode('+i+',true)">Manual</button>'+
      '</div>'+
      controlRow+
      calSection+
      calResultRow+
    '</div>';
  }).join('');
}

function renderCurveCards(){
  if(!cfg) return;
  document.getElementById('curve-cards').innerHTML = cfg.curves.map((c,i) => {
    let out = '';
    if(c.type==='linear' && sts){
      const sd=sts.sensors.find(s=>s.name===c.sensor);
      if(sd){
        let v;
        if(sd.value<=c.min_temp) v=c.min_pwm;
        else if(sd.value>=c.max_temp) v=c.max_pwm;
        else { const r=(sd.value-c.min_temp)/(c.max_temp-c.min_temp); v=Math.round(c.min_pwm+r*(c.max_pwm-c.min_pwm)); }
        out=fmtSensorVal(sd.value,sd.unit)+' \u2192 '+p2pct(v)+'%';
      }
    } else if(c.type==='fixed'){
      out=p2pct(c.value)+'%';
    } else if(c.type==='mix'){
      out=c.function+'('+(c.sources||[]).join(', ')+')';
    }
    return '<div class="card curve-card'+(i===selIdx?' active':'')+'" onclick="selectCurve('+i+')">'+
      '<div class="card-header"><span class="card-name">'+esc(c.name)+'</span>'+
        '<span class="type-badge type-'+c.type+'">'+c.type.toUpperCase()+'</span></div>'+
      miniSVG(c)+
      '<div class="card-output">'+out+'</div>'+
    '</div>';
  }).join('');
}

function miniSVG(c){
  if(c.type==='linear'){
    const y1=38-(c.min_pwm/255)*33, y2=38-(c.max_pwm/255)*33;
    return '<svg viewBox="0 0 100 42" class="mini-graph">'+
      '<polyline points="0,'+y1+' '+c.min_temp+','+y1+' '+c.max_temp+','+y2+' 100,'+y2+
      '" fill="none" style="stroke:var(--teal)" stroke-width="2" stroke-linecap="round"/></svg>';
  }
  if(c.type==='fixed'){
    const y=38-(c.value/255)*33;
    return '<svg viewBox="0 0 100 42" class="mini-graph">'+
      '<line x1="0" y1="'+y+'" x2="100" y2="'+y+'" style="stroke:var(--blue)" stroke-width="2"/></svg>';
  }
  return '';
}

// ── Sensor rename / delete ──

function renameSensor(idx, el){
  if(!cfg) return;
  const newName = el.value.trim();
  const orig = el.dataset.orig;
  if(!newName){ el.value=orig; return; }
  if(orig===newName) return;
  if(cfg.sensors.some(s=>s.name===newName && s.name!==orig)){
    notify('Sensor name "'+newName+'" already exists','error'); el.value=orig; return;
  }
  cfg.sensors[idx].name = newName;
  el.dataset.orig = newName;
  // Update curve references
  cfg.curves.forEach(c=>{ if(c.sensor===orig) c.sensor=newName; });
  markDirty(); renderCurveCards(); renderHardware();
}

function deleteSensor(idx){
  if(!cfg) return;
  const nm = cfg.sensors[idx].name;
  const used = cfg.curves.find(c=>c.sensor===nm);
  if(used){ notify('"'+nm+'" is used by curve "'+used.name+'"','error'); return; }
  cfg.sensors.splice(idx,1);
  markDirty(); renderSensorCards(); renderHardware(); renderCurveCards(); renderEditor();
}

// ── Fan rename ──

function renameFan(ctrlIdx, el){
  if(!cfg) return;
  const newName = el.value.trim();
  const orig = el.dataset.orig;
  if(!newName){ el.value=orig; return; }
  if(orig===newName) return;
  if(cfg.fans.some(f=>f.name===newName && f.name!==orig)){
    notify('Fan name "'+newName+'" already exists','error'); el.value=orig; return;
  }
  const fan = cfg.fans.find(f=>f.name===orig);
  if(!fan){ el.value=orig; return; }
  fan.name = newName;
  cfg.controls[ctrlIdx].fan = newName;
  el.dataset.orig = newName;
  markDirty();
}

// ── Manual PWM ──

function setManualMode(i, isManual){
  if(!cfg) return;
  if(isManual){
    cfg.controls[i].manual_pwm = 128; // default 50%
  } else {
    delete cfg.controls[i].manual_pwm;
  }
  markDirty(); renderFanCards();
}

function updManualPWM(i, pct, sliderEl){
  if(!cfg) return;
  cfg.controls[i].manual_pwm = Math.round(pct/100*255);
  // Update the sibling span without re-rendering
  if(sliderEl){
    const span = sliderEl.nextElementSibling;
    if(span) span.textContent = pct+'%';
  }
  markDirty();
}

// ── Calibration ──

async function startCalibration(pwmPath){
  if(!pwmPath) return;
  try {
    const r = await fetch('/api/calibrate/start?fan='+encodeURIComponent(pwmPath),{method:'POST'});
    if(r.ok){
      notify('Calibration started — fan will ramp up over ~40s','ok');
      loadCalibration();
      // poll more frequently while calibrating
      const poll = setInterval(async()=>{
        await loadCalibration();
        const st = calStatuses[pwmPath];
        if(!st || !st.running) clearInterval(poll);
      }, 3000);
    } else { notify(await r.text(),'error'); }
  } catch(e){ notify('Calibrate: '+e.message,'error'); }
}

async function detectRPM(pwmPath, ctrlIdx, btn){
  if(!pwmPath || !cfg) return;
  const orig = btn.textContent;
  btn.disabled = true;
  btn.textContent = '\u23f3';
  notify('Detecting RPM sensor\u2026 (~5s)', 'ok');
  try {
    const r = await fetch('/api/detect-rpm?fan='+encodeURIComponent(pwmPath), {method:'POST'});
    if(!r.ok){ notify(await r.text(),'error'); return; }
    const res = await r.json();
    if(!res.rpm_path){
      notify('No RPM sensor correlated with this fan \u2014 check wiring or fan type','error');
      return;
    }
    // Update the fan config with the detected rpm_path.
    const fan = cfg.fans.find(f=>f.name===cfg.controls[ctrlIdx].fan);
    if(fan){
      fan.rpm_path = res.rpm_path;
      // Show a short human-readable name: last two path segments
      const parts = res.rpm_path.split('/');
      const label = parts.slice(-2).join('/');
      notify('Detected: '+label+' (\u0394'+res.delta+' RPM) \u2014 hit Apply to save','ok');
      markDirty(); renderFanCards();
    }
  } catch(e){ notify('Detect: '+e.message,'error'); }
  finally { btn.disabled=false; btn.textContent=orig; }
}

// ── Editor ──

function selectCurve(i){ selIdx=i; renderCurveCards(); renderEditor(); }

function renderEditor(){
  const el=document.getElementById('curve-editor');
  if(!cfg||selIdx<0||selIdx>=cfg.curves.length){ el.innerHTML=''; return; }
  const c=cfg.curves[selIdx];
  if(c.type==='linear') renderLinearEditor(el,c);
  else if(c.type==='fixed') renderFixedEditor(el,c);
  else if(c.type==='mix') renderMixEditor(el,c);
}

function curveAxisLabel(c){
  if(!cfg || !c.sensor) return 'Value';
  const s = cfg.sensors.find(x=>x.name===c.sensor);
  const u = s ? sensorUnit(s) : '';
  if(u==='°C') return '°C';
  if(u==='%') return '%';
  if(u==='W') return 'W';
  if(u==='V') return 'V';
  if(u==='RPM') return 'RPM';
  if(u==='MHz') return 'MHz';
  return 'Value';
}

function renderLinearEditor(el,c){
  const sOpts=cfg.sensors.map(s =>
    '<option value="'+esc(s.name)+'"'+(s.name===c.sensor?' selected':'')+'>'+esc(s.name)+'</option>'
  ).join('');
  const ax = curveAxisLabel(c);
  el.innerHTML = '<div class="editor">'+
    '<div class="editor-svg"><svg id="curve-svg" viewBox="0 0 510 255" xmlns="http://www.w3.org/2000/svg"></svg></div>'+
    '<div class="editor-form">'+
      '<div class="fg"><label>Name</label><input type="text" value="'+esc(c.name)+'" onchange="renameCurve(this.value)"></div>'+
      '<div class="fg"><label>Sensor</label><select onchange="updField(\'sensor\',this.value)">'+sOpts+'</select></div>'+
      '<div class="fg"></div>'+
      '<div class="fg"><label>Min '+ax+'</label><input type="number" id="f-mint" value="'+c.min_temp+'" min="0" max="10000" step="1" onchange="updField(\'min_temp\',+this.value)"></div>'+
      '<div class="fg"><label>Max '+ax+'</label><input type="number" id="f-maxt" value="'+c.max_temp+'" min="0" max="10000" step="1" onchange="updField(\'max_temp\',+this.value)"></div>'+
      '<div class="fg"></div>'+
      '<div class="fg"><label>Min %</label><input type="number" id="f-minp" value="'+p2pct(c.min_pwm)+'" min="0" max="100" step="1" onchange="updPctField(\'min_pwm\',+this.value)"></div>'+
      '<div class="fg"><label>Max %</label><input type="number" id="f-maxp" value="'+p2pct(c.max_pwm)+'" min="0" max="100" step="1" onchange="updPctField(\'max_pwm\',+this.value)"></div>'+
      '<div class="fg"></div>'+
    '</div>'+
    '<div class="editor-actions"><button class="danger" onclick="deleteCurve()">Delete</button></div>'+
  '</div>';
  drawSVG(c);
}

function renderFixedEditor(el,c){
  const pct=p2pct(c.value||0);
  el.innerHTML = '<div class="editor">'+
    '<div class="editor-form">'+
      '<div class="fg"><label>Name</label><input type="text" value="'+esc(c.name)+'" onchange="renameCurve(this.value)"></div>'+
      '<div class="fg wide"><label>Speed %</label>'+
        '<div class="fixed-slider">'+
          '<input type="range" min="0" max="100" step="1" value="'+pct+'" oninput="updFixedPct(+this.value)">'+
          '<input type="number" min="0" max="100" step="1" value="'+pct+'" style="width:55px" onchange="updFixedPct(+this.value)">'+
          '<span class="pct">'+pct+'%</span>'+
        '</div></div>'+
    '</div>'+
    '<div class="editor-actions"><button class="danger" onclick="deleteCurve()">Delete</button></div>'+
  '</div>';
}

function renderMixEditor(el,c){
  const fOpts=['max','min','average'].map(f =>
    '<option value="'+f+'"'+(f===c.function?' selected':'')+'>'+f+'</option>'
  ).join('');
  const avail=cfg.curves.filter(x=>x.name!==c.name);
  const srcs=avail.map(x =>
    '<label><input type="checkbox" value="'+esc(x.name)+'" '+
    ((c.sources||[]).includes(x.name)?'checked':'')+
    ' onchange="updMixSources()"> '+esc(x.name)+'</label>'
  ).join('');
  el.innerHTML = '<div class="editor">'+
    '<div class="editor-form">'+
      '<div class="fg"><label>Name</label><input type="text" value="'+esc(c.name)+'" onchange="renameCurve(this.value)"></div>'+
      '<div class="fg"><label>Function</label><select onchange="updField(\'function\',this.value)">'+fOpts+'</select></div>'+
      '<div class="fg"><label>Sources (min 2)</label><div class="source-list" id="mix-sources">'+srcs+'</div></div>'+
    '</div>'+
    '<div class="editor-actions"><button class="danger" onclick="deleteCurve()">Delete</button></div>'+
  '</div>';
}

// ── SVG ──

function drawSVG(c){
  const svg=document.getElementById('curve-svg');
  if(!svg) return;
  const ax = curveAxisLabel(c);
  let h='<rect width="510" height="255" style="fill:var(--bg)" rx="4"/>';

  for(let t=0;t<=100;t+=20){
    const x=v2x(t);
    h+='<line x1="'+x+'" y1="'+G.t+'" x2="'+x+'" y2="'+G.b+'" style="stroke:var(--border2)"/>';
    h+='<text x="'+x+'" y="'+(G.b+12)+'" style="fill:var(--fg3)" font-size="9" text-anchor="middle" font-family="monospace">'+t+(ax==='°C'?'\u00b0':'')+'</text>';
  }
  const pg=[0,64,128,191,255],pl=['0','25','50','75','100'];
  for(let i=0;i<pg.length;i++){
    const y=p2y(pg[i]);
    h+='<line x1="'+G.l+'" y1="'+y+'" x2="'+G.r+'" y2="'+y+'" style="stroke:var(--border2)"/>';
    h+='<text x="'+(G.l-4)+'" y="'+(y+3)+'" style="fill:var(--fg3)" font-size="9" text-anchor="end" font-family="monospace">'+pl[i]+'%</text>';
  }

  const x1=v2x(0),y1=p2y(c.min_pwm),x2=v2x(c.min_temp),y2=p2y(c.min_pwm);
  const x3=v2x(c.max_temp),y3=p2y(c.max_pwm),x4=v2x(100),y4=p2y(c.max_pwm);
  h+='<path d="M'+x1+','+y1+' L'+x2+','+y2+'" fill="none" style="stroke:var(--border)" stroke-width="1.5" stroke-dasharray="3"/>';
  h+='<line x1="'+x2+'" y1="'+y2+'" x2="'+x3+'" y2="'+y3+'" style="stroke:var(--teal)" stroke-width="2.5" stroke-linecap="round"/>';
  h+='<path d="M'+x3+','+y3+' L'+x4+','+y4+'" fill="none" style="stroke:var(--border)" stroke-width="1.5" stroke-dasharray="3"/>';

  if(sts && c.sensor){
    const sd=sts.sensors.find(s=>s.name===c.sensor);
    if(sd){
      const sv=Math.min(sd.value,100);
      const sx=v2x(sv);
      h+='<line x1="'+sx+'" y1="'+G.t+'" x2="'+sx+'" y2="'+G.b+'" style="stroke:var(--amber)" stroke-width="1" stroke-dasharray="4,2"/>';
      let op;
      if(sd.value<=c.min_temp) op=c.min_pwm;
      else if(sd.value>=c.max_temp) op=c.max_pwm;
      else { const r=(sd.value-c.min_temp)/(c.max_temp-c.min_temp); op=c.min_pwm+r*(c.max_pwm-c.min_pwm); }
      h+='<circle cx="'+sx+'" cy="'+p2y(op)+'" r="4" style="fill:var(--amber)"/>';
      h+='<text x="'+(sx>300?sx-6:sx+6)+'" y="'+(p2y(op)-6)+'" style="fill:var(--amber)" font-size="9" text-anchor="'+(sx>300?'end':'start')+'" font-family="monospace">'+fmtSensorVal(sd.value,sd.unit)+'\u2192'+p2pct(Math.round(op))+'%</text>';
    }
  }

  h+='<circle class="ctrl-point" data-point="min" cx="'+x2+'" cy="'+y2+'" r="6" style="fill:var(--teal);stroke:var(--bg2);cursor:grab" stroke-width="1.5"/>';
  h+='<circle class="ctrl-point" data-point="max" cx="'+x3+'" cy="'+y3+'" r="6" style="fill:var(--blue);stroke:var(--bg2);cursor:grab" stroke-width="1.5"/>';
  svg.innerHTML=h;
}

// ── Field updates ──

function updField(f,v){
  if(selIdx<0) return;
  cfg.curves[selIdx][f]=v; markDirty(); renderEditor(); renderCurveCards();
}
function updPctField(f,pct){
  if(selIdx<0 || isNaN(pct)) return;
  cfg.curves[selIdx][f]=pct2p(pct); markDirty(); renderEditor(); renderCurveCards();
}
function updFixedPct(pct){
  if(selIdx<0) return;
  cfg.curves[selIdx].value=pct2p(pct);
  markDirty();
  renderFixedEditor(document.getElementById('curve-editor'),cfg.curves[selIdx]);
  renderCurveCards();
}
function updMixSources(){
  if(selIdx<0) return;
  const cks=document.querySelectorAll('#mix-sources input:checked');
  cfg.curves[selIdx].sources=Array.from(cks).map(c=>c.value);
  markDirty();
}
function updCtrl(i,v){ cfg.controls[i].curve=v; markDirty(); }

function renameCurve(n){
  if(selIdx<0) return;
  const old=cfg.curves[selIdx].name;
  if(old===n) return;
  if(cfg.curves.some(c=>c.name===n)){ notify('Name "'+n+'" exists','error'); renderEditor(); return; }
  cfg.controls.forEach(c=>{ if(c.curve===old) c.curve=n; });
  cfg.curves.forEach(c=>{ if(c.type==='mix'&&c.sources) c.sources=c.sources.map(s=>s===old?n:s); });
  cfg.curves[selIdx].name=n;
  markDirty(); renderCurveCards(); renderFanCards();
}

// ── CRUD ──

function addCurve(type){
  if(!cfg) return;
  let nm='new_'+type; let n=1;
  while(cfg.curves.some(c=>c.name===nm)) nm='new_'+type+'_'+n++;
  const c={name:nm,type:type};
  if(type==='linear'){
    c.sensor=cfg.sensors.length?cfg.sensors[0].name:'';
    c.min_temp=40; c.max_temp=80; c.min_pwm=30; c.max_pwm=255;
  } else if(type==='fixed'){
    c.value=128;
  } else if(type==='mix'){
    c.function='max'; c.sources=[];
  }
  cfg.curves.push(c); selIdx=cfg.curves.length-1;
  markDirty(); render();
}

function autoCurve(){
  if(!sts||!cfg||!cfg.sensors.length){ notify('No sensor data','error'); return; }
  const s=cfg.sensors[0];
  const sd=sts.sensors.find(x=>x.name===s.name);
  const cur=sd?sd.value:40;
  const minT=Math.max(25,Math.round((cur-5)/5)*5);
  let nm=s.name+'_auto'; let n=1;
  while(cfg.curves.some(c=>c.name===nm)) nm=s.name+'_auto_'+n++;
  cfg.curves.push({name:nm,type:'linear',sensor:s.name,min_temp:minT,max_temp:85,min_pwm:30,max_pwm:255});
  selIdx=cfg.curves.length-1; markDirty(); render();
  notify('Auto: '+s.name+' idle \u2248'+fmtSensorVal(cur, sd?sd.unit:'°C'),'ok');
}

function deleteCurve(){
  if(selIdx<0) return;
  const nm=cfg.curves[selIdx].name;
  const rc=cfg.controls.find(c=>c.curve===nm);
  if(rc){ notify('In use by '+rc.fan,'error'); return; }
  const rm=cfg.curves.find(c=>c.type==='mix'&&(c.sources||[]).includes(nm));
  if(rm){ notify('Source of '+rm.name,'error'); return; }
  cfg.curves.splice(selIdx,1);
  selIdx=Math.min(selIdx,cfg.curves.length-1);
  markDirty(); render();
}

// ── Drag ──

function svgPt(e){
  const svg=document.getElementById('curve-svg');
  if(!svg) return null;
  const pt=svg.createSVGPoint();
  const ev=e.touches?e.touches[0]:e;
  pt.x=ev.clientX; pt.y=ev.clientY;
  return pt.matrixTransform(svg.getScreenCTM().inverse());
}
function onDrag(e){
  if(!dragging||selIdx<0) return;
  e.preventDefault();
  const pt=svgPt(e); if(!pt) return;
  const c=cfg.curves[selIdx];
  if(dragging==='min'){
    c.min_temp=Math.min(x2v(pt.x),c.max_temp-1);
    c.min_pwm=y2p(pt.y);
  } else {
    c.max_temp=Math.max(x2v(pt.x),c.min_temp+1);
    c.max_pwm=y2p(pt.y);
  }
  markDirty(); drawSVG(c); renderCurveCards();
  const mt=document.getElementById('f-mint'),Mt=document.getElementById('f-maxt');
  const mp=document.getElementById('f-minp'),Mp=document.getElementById('f-maxp');
  if(mt)mt.value=c.min_temp; if(Mt)Mt.value=c.max_temp;
  if(mp)mp.value=p2pct(c.min_pwm); if(Mp)Mp.value=p2pct(c.max_pwm);
}
function endDrag(){
  dragging=null;
  document.removeEventListener('mousemove',onDrag);
  document.removeEventListener('mouseup',endDrag);
  document.removeEventListener('touchmove',onDrag);
  document.removeEventListener('touchend',endDrag);
}
document.addEventListener('mousedown',e=>{
  if(!e.target.classList.contains('ctrl-point')) return;
  e.preventDefault(); dragging=e.target.dataset.point;
  document.addEventListener('mousemove',onDrag);
  document.addEventListener('mouseup',endDrag);
});
document.addEventListener('touchstart',e=>{
  if(!e.target.classList.contains('ctrl-point')) return;
  e.preventDefault(); dragging=e.target.dataset.point;
  document.addEventListener('touchmove',onDrag,{passive:false});
  document.addEventListener('touchend',endDrag);
},{passive:false});

// ── Settings ──

function openSettings(){
  const el = document.getElementById('settings-overlay');
  el.style.display = 'flex';
  document.getElementById('settings-status').textContent = '';
  document.querySelector('#settings-overlay .danger').disabled = false;
}
function closeSettings(){
  document.getElementById('settings-overlay').style.display = 'none';
}
// Close on backdrop click
document.getElementById('settings-overlay').addEventListener('click', e => {
  if(e.target === document.getElementById('settings-overlay')) closeSettings();
});

async function confirmReset(){
  if(!confirm('This will delete the current configuration and restart the daemon. Continue?')) return;
  document.getElementById('settings-status').textContent = 'Resetting…';
  document.querySelector('#settings-overlay .danger').disabled = true;
  try {
    const r = await fetch('/api/setup/reset', {method:'POST'});
    if(!r.ok) throw new Error(await r.text());
  } catch(e){
    document.getElementById('settings-status').textContent = 'Error: '+e.message;
    document.querySelector('#settings-overlay .danger').disabled = false;
    return;
  }
  document.getElementById('settings-status').textContent = 'Restarting daemon…';
  // Poll /api/ping (unauthenticated) — session is lost on restart.
  await new Promise(r=>setTimeout(r,1200));
  const poll = setInterval(async()=>{
    try {
      const r = await fetch('/api/ping');
      if(r.ok){ clearInterval(poll); window.location.reload(); }
    } catch(_){}
  }, 800);
}

// ── Setup Wizard ──

let setupPollTimer = null;
let setupLastInstallLogLen = 0;

async function checkSetup(){
  try {
    const r = await fetch('/api/setup/status');
    const p = await r.json();
    if(p.needed && !p.applied){
      document.getElementById('setup-overlay').classList.remove('hidden');
      if(p.running){
        // Already running (e.g. page reload mid-setup) — attach poller.
        document.getElementById('setup-phase-area').style.display = '';
        renderSetupProgress(p);
        if(!setupPollTimer) setupPollTimer = setInterval(pollSetupStatus, 800);
      } else if(p.done){
        renderSetupProgress(p);
      } else {
        // Auto-start the wizard — user never needs to click anything.
        setupAutoStart();
      }
    } else {
      document.getElementById('setup-overlay').classList.add('hidden');
    }
  } catch(e){
    document.getElementById('setup-overlay').classList.add('hidden');
  }
}

async function setupAutoStart(){
  try {
    const r = await fetch('/api/setup/start',{method:'POST'});
    if(!r.ok){ return; } // already running, or error — poll will sort it out
  } catch(e){ return; }
  document.getElementById('setup-phase-area').style.display = '';
  setupPollTimer = setInterval(pollSetupStatus, 800);
}

async function pollSetupStatus(){
  try {
    const r = await fetch('/api/setup/status');
    const p = await r.json();
    renderSetupProgress(p);
    if(p.done || p.reboot_needed || (p.error && !p.running)){
      clearInterval(setupPollTimer);
      setupPollTimer = null;
    }
  } catch(e){}
}

async function doReboot(){
  const btn = document.getElementById('btn-reboot');
  const status = document.getElementById('reboot-status');
  btn.disabled = true;
  status.textContent = 'Sending reboot command…';
  try {
    await fetch('/api/system/reboot', {method:'POST'});
  } catch(e){}
  status.textContent = 'Rebooting… this page will reload automatically.';
  // Poll until the server comes back, then reload.
  const poll = setInterval(async () => {
    try {
      await fetch('/api/ping');
      clearInterval(poll);
      location.reload();
    } catch(e){}
  }, 3000);
}

function setupPhaseBadge(phase, label){
  const cls = phase === 'n/a' ? 'phase-na' : 'phase-'+phase;
  return '<span class="phase-badge '+cls+'">'+(label||phase)+'</span>';
}

function renderSetupProgress(p){
  // ── Phase status line ───────────────────────────────────────────────────
  const phaseArea = document.getElementById('setup-phase-area');
  const fanTablePhases = ['scanning_fans','detecting_rpm','calibrating','finalizing'];
  const showingFans = fanTablePhases.includes(p.phase) || (p.done && !p.error);

  if(p.running || (p.phase && !p.done)){
    phaseArea.style.display = '';
    const statusEl = document.getElementById('setup-phase-status');
    statusEl.textContent = p.phase_msg || p.phase || '';

    // Board line — shown once board is known
    const boardEl = document.getElementById('setup-board-line');
    if(p.board){
      boardEl.textContent = p.board;
      boardEl.style.display = '';
    }

    // Chip line — shown during driver install
    const chipEl = document.getElementById('setup-chip-line');
    if(p.chip_name){
      chipEl.textContent = 'Installing driver for ' + p.chip_name + '…';
      chipEl.style.display = '';
    } else {
      chipEl.style.display = 'none';
    }

    // Install log — shown only during installing_driver phase
    const logEl = document.getElementById('setup-install-log');
    const lines = p.install_log || [];
    if(p.phase === 'installing_driver' && lines.length > 0){
      logEl.style.display = '';
      for(let i = setupLastInstallLogLen; i < lines.length; i++){
        const ln = document.createElement('div');
        ln.style.cssText = 'margin:1px 0;white-space:pre-wrap;word-break:break-all';
        ln.textContent = lines[i];
        logEl.appendChild(ln);
      }
      setupLastInstallLogLen = lines.length;
      logEl.scrollTop = logEl.scrollHeight;
    } else if(p.phase !== 'installing_driver'){
      setupLastInstallLogLen = 0; // reset for next potential install
    }
  }

  // ── Fan table ───────────────────────────────────────────────────────────
  const progressArea = document.getElementById('setup-progress-area');
  if(showingFans){
    progressArea.style.display = '';
    const tbody = document.getElementById('setup-fan-tbody');
    tbody.innerHTML = '';
    for(const f of (p.fans||[])){
      const row = document.createElement('tr');

      let detectHtml;
      switch(f.detect_phase){
        case 'n/a':       detectHtml = setupPhaseBadge('na','n/a'); break;
        case 'pending':   detectHtml = setupPhaseBadge('pending','pending'); break;
        case 'detecting': detectHtml = setupPhaseBadge('detecting','detecting…'); break;
        case 'found':     detectHtml = setupPhaseBadge('found','detected'); break;
        case 'none':      detectHtml = setupPhaseBadge('none','none'); break;
        default:          detectHtml = setupPhaseBadge('pending', f.detect_phase||'—');
      }

      let calHtml = setupPhaseBadge(f.cal_phase||'pending', f.cal_phase||'pending');
      if(f.cal_phase==='calibrating'){
        const pct = f.cal_progress||0;
        calHtml += '<div class="setup-prog-bar"><div class="fill" style="width:'+pct+'%"></div></div>';
      }

      let result = '—';
      if(f.cal_phase==='done'){
        result = 'start '+f.start_pwm+'/255 ('+Math.round(f.start_pwm/255*100)+'%)';
        if(f.type!=='nvidia' && f.max_rpm) result += ', '+f.max_rpm+' RPM';
      } else if(f.cal_phase==='error'){
        result = '<span style="color:var(--red)">'+esc(f.error)+'</span>';
      }

      row.innerHTML = '<td>'+esc(f.name)+'</td><td>'+esc(f.type)+'</td><td>'+detectHtml+'</td><td>'+calHtml+'</td><td>'+result+'</td>';
      tbody.appendChild(row);
    }
  } else {
    progressArea.style.display = 'none';
  }

  // ── Reboot required ─────────────────────────────────────────────────────
  const rebootPanel = document.getElementById('setup-reboot-panel');
  if(p.reboot_needed){
    document.getElementById('setup-reboot-msg').textContent = p.reboot_message || '';
    rebootPanel.style.display = '';
    phaseArea.style.display = 'none';
  } else {
    rebootPanel.style.display = 'none';
  }

  // ── Error ───────────────────────────────────────────────────────────────
  const errEl = document.getElementById('setup-error');
  if(p.error){
    errEl.textContent = 'Setup failed: '+p.error;
    errEl.style.display = '';
    progressArea.style.display = '';
  } else {
    errEl.style.display = 'none';
  }

  // ── Done — show Apply button ────────────────────────────────────────────
  if(p.done && !p.error && p.config){
    document.getElementById('setup-phase-status').textContent = 'Setup complete — review and apply your configuration.';
    document.getElementById('setup-chip-line').style.display = 'none';
    document.getElementById('setup-done-area').style.display = '';
    renderSetupSummary(p);
  }
}


function renderSetupSummary(p){
  const cfg = p.config || {};
  const prof = p.profile || {};
  let html = '';

  // Hardware profile section
  if(prof.cpu_model || prof.gpu_model){
    html += '<div class="hw-profile">';
    if(prof.cpu_model){
      let s = '<strong>CPU:</strong> <span class="val">'+esc(prof.cpu_model)+'</span>';
      if(prof.cpu_tdp_w)     s += '  |  TDP <span class="val">'+prof.cpu_tdp_w+'W</span>';
      if(prof.cpu_thermal_c) s += '  |  TjMax <span class="val">'+prof.cpu_thermal_c+'°C</span>';
      html += '<div class="hw-profile-row">'+s+'</div>';
    }
    if(prof.gpu_model){
      let s = '<strong>GPU:</strong> <span class="val">'+esc(prof.gpu_model)+'</span>';
      if(prof.gpu_power_w)   s += '  |  Power limit <span class="val">'+prof.gpu_power_w+'W</span>';
      if(prof.gpu_thermal_c) s += '  |  Thermal limit <span class="val">'+prof.gpu_thermal_c+'°C</span>';
      html += '<div class="hw-profile-row">'+s+'</div>';
    }
    html += '</div>';
  }

  // Curve design notes
  if(prof.curve_notes && prof.curve_notes.length){
    html += '<div class="curve-notes"><span style="color:var(--fg);font-size:0.8rem">Curve design</span><ul>';
    for(const n of prof.curve_notes) html += '<li>'+esc(n)+'</li>';
    html += '</ul></div>';
  }

  // Sensors
  if((cfg.sensors||[]).length){
    html += '<h3>Sensors</h3><ul>';
    for(const s of cfg.sensors||[]) html += '<li>Sensor <span>'+esc(s.name)+'</span> ('+s.type+')</li>';
    html += '</ul>';
  }

  // Fans
  if((cfg.fans||[]).length){
    html += '<h3>Fans ('+cfg.fans.length+')</h3><ul>';
    for(const f of cfg.fans||[]){
      let s = '<li>Fan <span>'+esc(f.name)+'</span> ('+f.type+')';
      if(f.min_pwm) s += ' — min '+f.min_pwm+'/255 ('+Math.round(f.min_pwm/255*100)+'%)';
      html += s+'</li>';
    }
    html += '</ul>';
  }

  // Curves
  if((cfg.curves||[]).length){
    html += '<h3>Curves</h3><ul>';
    for(const c of cfg.curves||[]){
      let s = '<li>Curve <span>'+esc(c.name)+'</span> ('+c.type+')';
      if(c.sensor) s += ' → sensor <span>'+esc(c.sensor)+'</span>';
      if(c.min_temp!=null && c.max_temp!=null) s += ', <span>'+c.min_temp+'–'+c.max_temp+'°C</span>';
      if(c.min_pwm!=null && c.max_pwm!=null)   s += ', PWM <span>'+c.min_pwm+'–'+c.max_pwm+'</span>';
      html += s+'</li>';
    }
    html += '</ul>';
  }

  document.getElementById('setup-summary').innerHTML = html;
}

async function setupApply(){
  document.getElementById('btn-setup-apply').disabled = true;
  document.getElementById('setup-apply-hint').textContent = 'Saving…';
  try {
    const r = await fetch('/api/setup/apply',{method:'POST'});
    if(!r.ok){ throw new Error(await r.text()); }
  } catch(e){
    document.getElementById('btn-setup-apply').disabled = false;
    document.getElementById('setup-apply-hint').textContent = 'Error: '+e.message;
    return;
  }
  // Config saved — daemon is restarting. Show status and poll until it's back.
  document.getElementById('setup-actions').style.display = 'none';
  document.getElementById('setup-restarting').style.display = '';
  let dots = 1;
  const dotsEl = document.getElementById('setup-restart-dots');
  const dotsTimer = setInterval(()=>{ dots=(dots%3)+1; dotsEl.textContent='.'.repeat(dots); }, 500);
  // Wait briefly for the daemon to begin its shutdown, then poll /api/ping.
  // We use /api/ping (unauthenticated) because the session cookie is lost
  // when the daemon restarts with a fresh in-memory session store.
  await new Promise(r=>setTimeout(r,1200));
  const poll = setInterval(async()=>{
    try {
      const r = await fetch('/api/ping');
      if(r.ok){
        clearInterval(poll);
        clearInterval(dotsTimer);
        // Daemon is up — reload so the browser lands on the login page.
        window.location.reload();
      }
    } catch(_){}
  }, 800);
}

// ── Init ──
document.getElementById('btn-apply').addEventListener('click',applyConfig);
checkSetup().then(()=>{
  const overlay = document.getElementById('setup-overlay');
  if(overlay.classList.contains('hidden')){
    // Normal mode: load dashboard immediately.
    loadConfig(); loadStatus(); loadHardware(); loadCalibration();
    setInterval(loadStatus,2000);
    setInterval(loadHardware,3000);
    setInterval(loadCalibration,5000);
  }
});

// ── Sidebar toggle ──
function toggleSidebar(){
  const sb = document.getElementById('sidebar');
  const collapsed = sb.classList.toggle('collapsed');
  try { localStorage.setItem('ventd-sidebar', collapsed ? '0' : '1'); } catch(_){}
}
(function(){
  try {
    if(localStorage.getItem('ventd-sidebar') === '0'){
      const sb = document.getElementById('sidebar');
      if(sb) sb.classList.add('collapsed');
    }
  } catch(_){}
})();

// ── Theme toggle ──
function applyTheme(theme){
  document.documentElement.setAttribute('data-theme', theme);
  // Update both toggle buttons (header + setup wizard)
  const icon = theme === 'light' ? '\u2600' : '\u263E'; // ☀ / ☾
  document.querySelectorAll('.theme-btn').forEach(b => b.textContent = icon);
}
function toggleTheme(){
  const next = document.documentElement.getAttribute('data-theme') === 'light' ? 'dark' : 'light';
  applyTheme(next);
  try { localStorage.setItem('ventd-theme', next); } catch(_){}
}
// Apply saved theme immediately (before first paint would flicker, but we're in a script at bottom).
(function(){
  let saved = 'dark';
  try { saved = localStorage.getItem('ventd-theme') || 'dark'; } catch(_){}
  applyTheme(saved);
})();
</script>
</body>
</html>`

const loginHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Ventd — Login</title>
<style>
:root {
  --bg:       #0d1117; --bg2: #161b22; --bg3: #21262d;
  --border:   #30363d;
  --fg:       #c9d1d9; --fg1: #e6edf3; --fg2: #8b949e; --fg3: #484f58;
  --teal:     #4fc3a1; --teal-h: #3da88a; --teal-bg: #1a3a2a;
  --red:      #f85149; --red-bg: #3a1a1a;
  --amber:    #e6a23c;
  --btn-fg:   #0d1117;
}
[data-theme="light"] {
  --bg:       #f6f8fa; --bg2: #ffffff; --bg3: #f3f4f6;
  --border:   #d0d7de;
  --fg:       #24292f; --fg1: #1f2328; --fg2: #57606a; --fg3: #8c959f;
  --teal:     #0b8a6e; --teal-h: #096e57; --teal-bg: #d1f5ee;
  --red:      #cf222e; --red-bg: #ffebe9;
  --amber:    #7d4e00;
  --btn-fg:   #ffffff;
}
*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
body {
  font-family: 'Consolas', 'Monaco', 'Courier New', monospace;
  background: var(--bg); color: var(--fg);
  font-size: 15px;
  min-height: 100vh;
  display: flex; flex-direction: column; align-items: center; justify-content: center;
  transition: background 0.2s, color 0.2s;
}
.card {
  background: var(--bg2); border: 1px solid var(--border);
  border-radius: 8px; padding: 2rem 2.2rem;
  width: 100%; max-width: 380px;
}
.brand {
  display: flex; align-items: center; gap: 10px;
  margin-bottom: 1.6rem;
}
.brand svg { flex-shrink: 0; }
.brand-text h1 { font-size: 1rem; color: var(--fg1); letter-spacing: 0.05em; }
.brand-text p  { font-size: 0.65rem; color: var(--fg3); text-transform: uppercase; letter-spacing: 0.12em; margin-top: 2px; }
.theme-btn {
  margin-left: auto; background: none; border: 1px solid var(--border);
  color: var(--fg2); padding: 4px 8px; border-radius: 4px;
  cursor: pointer; font-size: 1rem; line-height: 1;
}
.theme-btn:hover { border-color: var(--teal); color: var(--teal); }

h2 { font-size: 0.8rem; color: var(--fg2); text-transform: uppercase; letter-spacing: 0.1em; margin-bottom: 1.2rem; }

.field { margin-bottom: 1rem; }
.field label { display: block; font-size: 0.75rem; color: var(--fg2); margin-bottom: 0.3rem; }
.field input {
  width: 100%; padding: 0.5rem 0.7rem;
  background: var(--bg3); border: 1px solid var(--border);
  border-radius: 4px; color: var(--fg1); font-family: inherit; font-size: 0.9rem;
  outline: none; transition: border-color 0.15s;
}
.field input:focus { border-color: var(--teal); }

.btn {
  width: 100%; padding: 0.55rem; margin-top: 0.5rem;
  background: var(--teal); color: var(--btn-fg); border: none;
  border-radius: 4px; font-family: inherit; font-size: 0.9rem; font-weight: bold;
  cursor: pointer; transition: background 0.15s;
}
.btn:hover { background: var(--teal-h); }
.btn:disabled { opacity: 0.5; cursor: default; }

.msg {
  margin-top: 0.9rem; padding: 0.5rem 0.7rem;
  border-radius: 4px; font-size: 0.8rem; display: none;
}
.msg.err { background: var(--red-bg); color: var(--red); display: block; }
.msg.ok  { background: var(--teal-bg); color: var(--teal); display: block; }

.section { display: none; }
.section.active { display: block; }

.hint { font-size: 0.72rem; color: var(--fg3); margin-top: 1.1rem; line-height: 1.5; }
.hint code {
  background: var(--bg3); border: 1px solid var(--border);
  border-radius: 3px; padding: 1px 5px; color: var(--amber);
}
</style>
</head>
<body>
<div class="card">
  <div class="brand">
    <svg width="28" height="28" viewBox="0 0 28 28" fill="none" xmlns="http://www.w3.org/2000/svg">
      <circle cx="14" cy="14" r="13" stroke="#4fc3a1" stroke-width="1.5" fill="none"/>
      <circle cx="14" cy="14" r="3" fill="#4fc3a1"/>
      <line x1="14" y1="4"  x2="14" y2="9"  stroke="#4fc3a1" stroke-width="2" stroke-linecap="round"/>
      <line x1="14" y1="19" x2="14" y2="24" stroke="#4fc3a1" stroke-width="2" stroke-linecap="round"/>
      <line x1="4"  y1="14" x2="9"  y2="14" stroke="#4fc3a1" stroke-width="2" stroke-linecap="round"/>
      <line x1="19" y1="14" x2="24" y2="14" stroke="#4fc3a1" stroke-width="2" stroke-linecap="round"/>
      <line x1="7.1"  y1="7.1"  x2="10.7" y2="10.7" stroke="#4fc3a1" stroke-width="2" stroke-linecap="round"/>
      <line x1="17.3" y1="17.3" x2="20.9" y2="20.9" stroke="#4fc3a1" stroke-width="2" stroke-linecap="round"/>
      <line x1="20.9" y1="7.1"  x2="17.3" y2="10.7" stroke="#4fc3a1" stroke-width="2" stroke-linecap="round"/>
      <line x1="10.7" y1="17.3" x2="7.1"  y2="20.9" stroke="#4fc3a1" stroke-width="2" stroke-linecap="round"/>
    </svg>
    <div class="brand-text">
      <h1>Ventd</h1>
      <p>System Fan Controller</p>
    </div>
    <button class="theme-btn" id="themeBtn" title="Toggle theme">◐</button>
  </div>

  <!-- Section A: normal login -->
  <div class="section active" id="secLogin">
    <h2>Sign In</h2>
    <div class="field">
      <label for="password">Password</label>
      <input type="password" id="password" autocomplete="current-password" placeholder="••••••••">
    </div>
    <button class="btn" id="loginBtn">Sign In</button>
    <div class="msg" id="loginMsg"></div>
    <p class="hint">First time? Check the terminal / <code>journalctl -u ventd</code> for your setup token.</p>
  </div>

  <!-- Section B: first-boot — enter setup token + set password -->
  <div class="section" id="secFirstBoot">
    <h2>First Boot Setup</h2>
    <div class="field">
      <label for="setupToken">Setup Token</label>
      <input type="text" id="setupToken" autocomplete="off" placeholder="XXXXX-XXXXX-XXXXX" spellcheck="false">
    </div>
    <div class="field">
      <label for="newPassword">Create Password <span style="color:var(--fg3)">(min 8 chars)</span></label>
      <input type="password" id="newPassword" autocomplete="new-password" placeholder="••••••••">
    </div>
    <div class="field">
      <label for="confirmPassword">Confirm Password</label>
      <input type="password" id="confirmPassword" autocomplete="new-password" placeholder="••••••••">
    </div>
    <button class="btn" id="firstBootBtn">Create Password &amp; Continue</button>
    <div class="msg" id="firstBootMsg"></div>
    <p class="hint">Find your setup token in the terminal or via:<br><code>journalctl -u ventd | grep "Setup token"</code></p>
  </div>
</div>

<script>
(function() {
  // Theme
  function applyTheme(t) {
    document.documentElement.setAttribute('data-theme', t);
    document.getElementById('themeBtn').textContent = t === 'dark' ? '◑' : '◐';
  }
  let theme = 'dark';
  try { theme = localStorage.getItem('ventd-theme') || 'dark'; } catch(_){}
  applyTheme(theme);
  document.getElementById('themeBtn').addEventListener('click', function() {
    theme = theme === 'dark' ? 'light' : 'dark';
    applyTheme(theme);
    try { localStorage.setItem('ventd-theme', theme); } catch(_){}
  });

  // Detect first-boot by probing /api/ping then checking 401 response
  // We use a heuristic: if normal login fails with a specific first-boot flag,
  // switch views. For simplicity, we show the first-boot section if localStorage
  // has a flag, or if the login attempt returns first_boot=true.
  var isFirstBoot = false;

  function showMsg(el, text, isErr) {
    el.textContent = text;
    el.className = 'msg ' + (isErr ? 'err' : 'ok');
  }

  // Normal login
  document.getElementById('password').addEventListener('keydown', function(e) {
    if (e.key === 'Enter') document.getElementById('loginBtn').click();
  });

  document.getElementById('loginBtn').addEventListener('click', function() {
    var btn = this;
    var pw = document.getElementById('password').value;
    var msg = document.getElementById('loginMsg');
    btn.disabled = true; btn.textContent = 'Signing in…';

    var body = new URLSearchParams();
    body.append('password', pw);

    fetch('/login', { method: 'POST', body: body })
      .then(function(r) { return r.json().then(function(j) { return {status: r.status, body: j}; }); })
      .then(function(res) {
        if (res.status === 200) {
          // Redirect to intended destination or root
          var dest = new URLSearchParams(location.search).get('next') || '/';
          location.href = dest;
          return;
        }
        if (res.status === 401 && res.body.first_boot) {
          // Server says no password set yet — switch to first-boot UI
          document.getElementById('secLogin').classList.remove('active');
          document.getElementById('secFirstBoot').classList.add('active');
          return;
        }
        showMsg(msg, res.body.error || 'Login failed', true);
        btn.disabled = false; btn.textContent = 'Sign In';
      })
      .catch(function() {
        showMsg(msg, 'Network error — is the daemon running?', true);
        btn.disabled = false; btn.textContent = 'Sign In';
      });
  });

  // First-boot submit
  document.getElementById('firstBootBtn').addEventListener('click', function() {
    var btn = this;
    var token   = document.getElementById('setupToken').value.trim();
    var pw      = document.getElementById('newPassword').value;
    var pw2     = document.getElementById('confirmPassword').value;
    var msg     = document.getElementById('firstBootMsg');

    if (!token) { showMsg(msg, 'Setup token is required', true); return; }
    if (pw.length < 8) { showMsg(msg, 'Password must be at least 8 characters', true); return; }
    if (pw !== pw2)    { showMsg(msg, 'Passwords do not match', true); return; }

    btn.disabled = true; btn.textContent = 'Setting up…';

    var body = new URLSearchParams();
    body.append('setup_token', token);
    body.append('new_password', pw);

    fetch('/login', { method: 'POST', body: body })
      .then(function(r) { return r.json().then(function(j) { return {status: r.status, body: j}; }); })
      .then(function(res) {
        if (res.status === 200) {
          showMsg(msg, 'Password set! Redirecting…', false);
          setTimeout(function() { location.href = '/'; }, 800);
          return;
        }
        showMsg(msg, res.body.error || 'Setup failed', true);
        btn.disabled = false; btn.textContent = 'Create Password & Continue';
      })
      .catch(function() {
        showMsg(msg, 'Network error', true);
        btn.disabled = false; btn.textContent = 'Create Password & Continue';
      });
  });

  // Auto-detect first-boot: check if there is no password hash configured
  // by attempting a login with empty password — server returns first_boot flag.
  fetch('/login', {
    method: 'POST',
    body: new URLSearchParams([['password', '']])
  })
    .then(function(r) { return r.json().then(function(j) { return {status: r.status, body: j}; }); })
    .then(function(res) {
      if (res.status === 401 && res.body.first_boot) {
        document.getElementById('secLogin').classList.remove('active');
        document.getElementById('secFirstBoot').classList.add('active');
      }
    })
    .catch(function() {});
})();
</script>
</body>
</html>`
