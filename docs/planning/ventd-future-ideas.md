# ventd — future ideas backlog

**Generated**: 2026-04-26 evening, end of v0.5.0 ship day.
**Read this**: tomorrow morning, after coffee, before opening Claude Code.
**Purpose**: capture ideas raised at the end of session so they're not lost. NOT a todo list. NOT priorities. Most of these are post-v1.0.

## How to read this list

The roadmap as designed (v0.5 → v0.6 PI → v0.7 FF+safety → v0.8 ARX → v0.9 acoustics → v1.0 predictive) gets ventd to a defensible product. Everything below is **scope additions**, not gaps in the roadmap.

Rule: **don't start any of these until v1.0 ships.** Adding scope before executing existing scope is the failure mode that burned the $600 weekend. The two project-hygiene items at the bottom are the only exceptions worth considering inside the v0.5.x window.

---

## Tier 1 — real user value, fits the vision

### 1. Notification / alert system
- Fan failure, stuck PWM, sensor out of range, calibration drift over time
- Currently silent on errors (firmware-restore happens, user doesn't know)
- Email / webhook / ntfy.sh push
- Aligns with TrueNAS/Unraid/Proxmox audience (already have notification frameworks)
- **Likely v0.7-0.8**

### 2. Quiet mode scheduling
- "Between 11pm and 7am, prefer sound over thermal headroom"
- Cap fan PWM at X%, allow temps to climb within safe limits
- Real homelab use case (NAS in bedroom)
- Trivial after PI controller (spec-04) lands
- **v0.6.x add-on after spec-04**

### 3. Multi-host coordination
- Homelab users with 3+ ventd boxes want one dashboard
- Not a control plane (CoolerControl's mistake) — just "this UI can connect to multiple hosts"
- Already half-shipped: host switcher mockup exists in the project folder
- **v0.7**

### 4. Power consumption tracking
- Higher PWM = more watts
- Track watts per fan, daily/monthly draw
- Solar/battery setups care
- Cheap to add, on-brand for "knows what your machine is doing"
- **v0.8+**

### 5. Acoustic profile import/export
- "I've tuned this to be silent under load, share the curve"
- Different from hardware profiles — just curves
- Lightweight community feature
- **v0.7**

---

## Tier 2 — useful but scope-creepy, defer hard

### 6. Mobile UI / PWA
- Web UI works on mobile, isn't *good*
- spec-12 should consider this; don't build a separate app

### 7. Prometheus / OpenMetrics export
- Homelab folks running Grafana would consume this
- ~100 lines of code
- **Easy v0.6.x add if requested**

### 8. SNMP support
- Enterprise IT framework
- Overlaps with Prometheus, more legacy
- **Skip unless explicitly requested**

### 9. ECC RAM monitoring
- Thermal events → ECC errors
- Out of scope. Don't.

### 10. Wake-on-LAN integration
- "Wake box, calibrate, sleep"
- Solving a problem nobody has

---

## Tier 3 — push back if anyone proposes these

### 11. Cloud sync of profiles to ventd's servers
- No. Violates "no phone home"

### 12. Built-in chat/IRC/Discord support
- No. ntfy webhooks cover this

### 13. AI-suggested fan curves
- No. The whole v1.0 thesis is *measured* predictive control
- AI-generated curves is the opposite of that

### 14. Plugin system
- No. Static binary is a feature
- Plugins kill the safety story

### 15. GUI (native desktop, Tauri/Electron)
- No. Web UI is the right primitive
- CoolerControl made the GUI choice — that's their differentiation
- Don't copy them

---

## Project hygiene — could act on within v0.5.x

These aren't features, they're operational gaps that pay off when external users arrive.

### A. Issue templates
- Bug report (prompts for ventd version, kernel, `ventd diag` output)
- Feature request (scope/use-case/alternatives)
- Hardware compatibility (board info + what works)
- Place in `.github/ISSUE_TEMPLATE/`
- **15 min, no CC**

### B. Discord / Matrix / forum
- Low-friction support channel for when r/homelab sees v0.5.0
- Not blocking, comes when users arrive

### C. Public roadmap as GitHub Project
- Currently lives in README + memory
- Public Now/Next/Later/Shipped board with linked specs
- **30 min, no CC**

### D. Comparison benchmarks
- "fan2go vs ventd: 100 reboots, calibration time, accuracy"
- Marketing artifact, drives adoption
- Separate repo, ~1 day's work
- Post on r/Linux when ready

### E. GitHub Sponsors
- You spend real money on Claude
- Sponsors offset without changing GPL/no-cloud stance
- **20 min setup**

### F. CONTRIBUTING.md content audit
- Check if it documents how to contribute or is a stub

---

## What to do tomorrow morning

**Don't act on this list.**

The clean target is **issue #655**: spec-14a bootstrap. Mechanical CC session, $5-10 Sonnet, 30-60 min. That's the right next move.

If after that you're still energetic and want a hygiene win: file project items A and C as `priority:p3` issues. That's *it*.

Everything else here is post-v1.0. Re-read this list when v1.0 ships. Re-evaluate then.

---

## Footer — sanity check

If you find yourself opening Claude Code at 9pm to start "just one of these," close the laptop. The shipped roadmap is good. Execute it. Add scope after, not during.
