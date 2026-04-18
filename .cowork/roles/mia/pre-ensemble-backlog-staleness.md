# Pre-ensemble backlog staleness classification

One-time pre-classification of the 18 pre-ensemble open issues (those filed before the Atlas/Cassidy/Mia ensemble activated on 2026-04-18). Built during session 3 downtime so the eventual 30-day stale-scrub pass (first eligibility: ~2026-05-16) is a fast verify-and-act rather than a full-read-and-decide.

Each issue gets one of three classifications:

- **ACTIVE** — has a clear owner or ongoing work stream. If still open at stale-scrub time, add a status-request comment and bump the clock.
- **BLOCKED-EXTERNAL** — open because of a real external dependency (hardware access, upstream release, cross-distro infrastructure). If still open at stale-scrub time, leave open without comment — it's idle for a good reason.
- **PROBABLY-ABANDONED** — filed with optimistic intent and no current activity. If still open at stale-scrub time, close as `not_planned` with rationale pointing at this classification.

Classifications reflect my read of issue bodies as of 2026-04-18. Re-evaluate at scrub time — the repo may have moved.

---

## Classification table

| # | Title (abbreviated) | Classification | Rationale |
|---|---|---|---|
| 68 | web(responsive): real-phone smoke pass | **BLOCKED-EXTERNAL** | Needs physical phones. v0.3.0 milestone-clear is blocking; once that's done, leave open as a "someone with a phone" ticket. |
| 129 | feat: NVMe/drive temperatures in dashboard | **PROBABLY-ABANDONED** | Filed 2026-04-16 as a "it would be nice" pointing at Storage group that's currently hidden. No PR, no activity. If v0.4+ scope, re-milestone; else close. |
| 132 | test(setup): extract calibrate.Manager interface | **ACTIVE** | Called out as follow-up from PR #136. Unblocks 7 skipped tests. Will be picked up in Phase 1 code cleanup; Atlas's lane. |
| 146 | test(monitor): GPU mock | **ACTIVE** | Filed by PR #138 as v0.4 scope with clear test plan. Real work item waiting for prioritisation; Atlas's lane. |
| 167 | rig: 0a.v reboot-survival PASS | **BLOCKED-EXTERNAL** | Needs physical access to phoenix-MS-7D25 rig + post-reboot verifier run. Not a code issue. Phoenix-only. |
| 171 | cross-distro smoke: Arch template (pacman Landlock) | **BLOCKED-EXTERNAL** | Needs access to pve host + throwaway Arch VM + cloud-init config. Infrastructure, not code. Atlas may dispatch when CI capacity allows. |
| 172 | cross-distro smoke: Void glibc template | **BLOCKED-EXTERNAL** | Needs glibc Void image on pve host (not yet cached). Infrastructure. Same bucket as #171. |
| 173 | cross-distro smoke: Alpine 3.19 cloud-init | **BLOCKED-EXTERNAL** | Needs Alpine cloud-init image on pve host. Infrastructure. Same bucket as #171/#172. |
| 179 | ui(session-C): Phase 2 — IA + daemon status | **ACTIVE** | Clear scope + gate (post-Phoenix-signoff). Serial with #180, #181. Will be picked up. |
| 180 | ui(session-D): Phase 3 — control depth | **ACTIVE** | Gated on #179. Clear scope. Will be picked up. |
| 181 | ui(session-E): Phase 4 + 4.5i — polish | **ACTIVE** | Gated on #180. Release-blocker for v0.3.0. Will be picked up before tag. |
| 182 | ux: first-boot security token surfacing | **ACTIVE** | Clear scope + acceptance criteria. Install-script UX fix. Atlas-dispatchable. |
| 183 | release: v0.2.0 → v0.3.0 upgrade-path | **ACTIVE** | Release-blocker for v0.3.0 tag. Five acceptance criteria across four distros. Phoenix-executable. |
| 184 | v0.3.0 pre-tag burn-down summary | **PROBABLY-ABANDONED** | Handoff meta-issue from 2026-04-16 CC session. Status is "informational only — all work items have their own issues." Close once the five PRs it references are all integrated (which they are, per body). |
| 215 | screenshots: v0.3 Session C Phase 2 UI | **BLOCKED-EXTERNAL** | Needs physical rig + screenshot capture. Phoenix-only. |
| 216 | test(web): panic + profile endpoints e2e | **ACTIVE** | Clear scope (6 endpoints × 4 test patterns). Session C follow-up. Atlas-dispatchable. |
| 228 | ci: build-and-test-arch flakes | **ACTIVE** | Known flake; has tracking comment from 2026-04-17. If still flaking at stale-scrub time, worth promoting to a fix-it dispatch. |
| 229 | docs: test-suite summary (post-#227 snapshot) | **PROBABLY-ABANDONED** | Snapshot doc from 2026-04-17. One-off — either the content is in `docs/test-suite-summary.md` (scrub says: close as completed) or it's a standalone issue body that's superseded by the next ultrareview (close as not_planned). |

## Summary by classification

- **ACTIVE (10)**: #132, #146, #179, #180, #181, #182, #183, #216, #228, and arguably more if context develops. Most will enter `role:atlas` dispatch rotation in Phase 2+ of the roadmap.
- **BLOCKED-EXTERNAL (6)**: #68, #167, #171, #172, #173, #215. Waiting on physical hardware or infrastructure. Don't scrub; don't comment.
- **PROBABLY-ABANDONED (3)**: #129, #184, #229. Scrub candidates if they hit 30-day idle; each has a clear close-rationale already written above.

## Scrub-time protocol

Run at the first stale-scrub pass (nearest candidate: #129 at 2026-05-16). For each issue:

1. Re-verify its activity timestamp. If updated within 30 days, leave open (reset clock).
2. If still idle >30 days, apply the action per its classification above:
   - ACTIVE → add a status-request comment tagging the likely owner (Atlas for code, Phoenix for hardware).
   - BLOCKED-EXTERNAL → leave open silently (no spam).
   - PROBABLY-ABANDONED → close as `not_planned` citing this file's classification.
3. If the classification no longer fits (context drift), re-evaluate and update this file.

## When to re-run this classification

- Each quarter, or
- When the backlog shape changes significantly (e.g. v0.3.0 ships, Phase 2 begins, or 5+ issues change state in a week).

Next scheduled re-evaluation: 2026-07-18 (quarterly) or earlier per event trigger.
