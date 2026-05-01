# R22 — Workload-signature persistence across kernel/userspace upgrades

**Status:** OPEN. Surfaced 2026-05-01 by the post-v0.5.6 smart-mode
audit (`/root/ventd-walkthrough/smart-mode-smarter.md` §"Open
research questions" #2).

**Question.** R7 hashes `/proc/PID/comm` under a per-install salt.
After a major distro upgrade, almost every comm shifts: new
systemd unit names, new browser binary path (e.g. `chrome` →
`chromium-browser` on Debian 13), new container runtimes
(`containerd` → `nerdctl`), Wayland session components
(`gnome-shell` → `gnome-shell-bin`). **Layer C invalidates
wholesale; the user re-warms from scratch over weeks.**

R7 §Q5 LRU eviction handles this *eventually* (TTL = 14 days), but
the user's first 14 days post-upgrade have a confidence-collapsed
controller running on Layer A's curve only — exactly the regime
where Layer C's marginal-benefit gating delivers the most value.

**Why R1-R20 don't answer this.**
- R7's signature stability analysis (§Q4) covers within-session
  flap (Steam launch, Chrome tab churn), not cross-session
  signature drift.
- R10's persistence (§Q5) preserves signatures across daemon
  restart but does NOT detect that "chrome" and "chromium-browser"
  are the same workload from the user's perspective.

**What needs answering.**
1. What signal best detects "this comm I'm seeing for the first
   time post-upgrade is actually the same workload as `chrome`
   I retired last week"?
2. Is per-install learned coalescence (label X observed CPU/RSS
   pattern correlates with retired label Y at α > 0.95 → coalesce)
   safe? Or does it leak privacy by reconstructing process
   identity through correlation?
3. Should ventd opt-in consume `/etc/os-release` upgrade events
   to mark a "signature-flush window" and warn the user?
4. Maintenance-class label dictionary (R7 §Q2 (B)) is hand-curated
   in `internal/idle/blocklist_export.go`; does it need a
   distro-aware extension (e.g. `apt` on Debian, `dnf` on Fedora,
   `pacman` on Arch — already in the list, but `paru` and `yay`
   are AUR-helper-specific)?

**Pre-requisite for.** Sustainable smart-mode UX over multi-year
ventd installs.

**Recommended target.** Post-v0.7.x; not blocking for v0.6.0.
Upgrade-driven re-warming is annoying but recoverable.

**Effort estimate.** 1 R-item + 1 spec patch (~v0.8.x). Research
~2 weeks (the privacy analysis is non-trivial), spec ~1 week,
implementation ~1 week.
