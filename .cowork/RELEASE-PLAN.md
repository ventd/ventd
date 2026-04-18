# ventd — release & branding plan

**Owner:** Cowork (review at every phase boundary, rewrite when invalidated)
**Last updated:** 2026-04-18
**Companion to:** `.cowork/STRATEGY.md` (what to build + why) and `ventdmasterplan.mkd` (when to build each piece)

This document answers: **when does the public face of ventd (README, website, release notes, social footprint) change, and what does it say at each stage?** It exists because the failure mode of underdocumented projects is the README drifting 6–18 months ahead of the code — which is how vaporware reads. The rule here is simple: the README never promises a capability until that capability is in a released binary.

---

## 1 · Releases — the schedule

Ventd follows semver. Major version = deep compatibility break (none planned before 1.0). Minor = new shipped capability. Patch = fixes against the current minor.

| Version | Trigger | README update scope | Release notes highlight | Social / branding moves |
|---------|---------|---------------------|-------------------------|-------------------------|
| **v0.3.0** | Ultrareview-1 passes + Phase 1 closed | Phase 1 note already there; add "v0.3.0 release" banner once tagged | HAL foundation, hot-loop optimisation, fingerprint hwdb, opt-in remote refresh | None beyond release post; project still pre-1.0 |
| **v0.4.0** | First Phase 2 backend merges + ultrareview-2 passes | Feature list adds the backend by name (e.g. "IPMI: native driver, no `ipmitool` dependency"). "How it compares" table adds column. Install section unchanged. | Whichever Phase 2 backend landed first (IPMI or LIQUID most likely) | Reddit post in the relevant subreddit (/r/homelab for IPMI, /r/buildapc or /r/sffpc for LIQUID) |
| **v0.5.0** | Three Phase 2 backends shipped (IPMI + LIQUID + CROSEC) + install path (Phase 3) stable | Features list shows three new fan families. Install section mentions coexistence detection. Screenshots may need regeneration. | "ventd now runs every fan in the building" — server (IPMI), desktop (LIQUID), laptop (CROSEC) | Blog post on ventd.org (when that exists): "One daemon, three fan families." Cross-post to HN. |
| **v0.6.0** | Phase 4 ships — PI controller + autotune (MPC opt-in as experimental flag) | Features list adds "Autotuning PI controller." "How it compares" table adds row. | Measurable quietness delta — publish benchmark results | First community call-to-action: "Run ventd on your weird hardware, open a hardware-report issue." |
| **v0.7.0** | Phase 5 ships — live calibration + profile capture + hardware-profiles DB populated with ~100 boards | README feature: "First-boot zero-click on matched hardware." Screenshot update: wizard showing one-click Apply. | The profile flywheel publicly begins. Acquisition story. | Dedicated ventd.org page for the profile DB. Open call for hardware reports. |
| **v0.8.0** | Phase 6-WIN lands — Windows binary, MSI installer, service | README gets cross-platform rewrite: install matrix for Windows. Screenshot set doubles (dashboard on Windows). "Supported platforms" section overhauled. | **Major branding moment.** Exploits the FanControl.Releases WinRing0 cliff. | Blog post specifically addressed to FanControl users — "An open-source option that runs without WinRing0." HN front page candidate. |
| **v0.9.0** | Phase 6-MAC lands — Intel + Apple Silicon | Screenshots triple. README opens with "The one fan controller for Linux, Windows, and Mac." Still pre-1.0 but the pitch is complete. | Second major branding moment. Apple Silicon is greenfield; claim it. | Mac-forum posts. Apple Silicon communities. Asahi Linux cross-promotion. |
| **v1.0.0** | Phase 6 complete (all platforms stable) + Phase 7 ships one advanced feature (probably MPC default-on or acoustic baseline) + 30 days without a high-severity regression | **Full README rewrite.** Drop the "Linux" framing from the tagline. Cross-platform compatibility matrix front and center. | **The release.** ventd 1.0 — one fan controller, every machine. | Launch campaign. ventd.org redesign. Product Hunt, HN, Reddit, niche forums all simultaneous. Conference talk submissions (FOSDEM, Linux Plumbers). |

### Interstitial releases (patches)

Patch releases (v0.3.1, v0.3.2) do not update the README. Their scope is bug fixes plus any finalisation work from the preceding minor. Release notes go in `CHANGELOG.md`; the README only moves on minor versions.

---

## 2 · README update protocol

Single rule: **the README at `HEAD` of `main` must match what's in the most recent release tag, plus any post-release bug fixes.** It does not describe work in progress.

Practical consequences:

- When a feature merges to main but hasn't shipped in a tag yet, it does **not** appear in the Features list. It can appear in the roadmap / "What's coming" section, with the phrase "currently in development" or "landed in main, next release."
- When a release tag is cut, the README receives a single commit that moves the shipped feature from "What's coming" into "Features," updates any affected comparison table, and bumps the version badge at the top if applicable.
- This README-update-commit is pre-drafted **before** the tag is pushed, so the tag release and the README update land within 60 seconds of each other.

### Template for the README-update-commit

```
docs(readme): update for v0.X.Y release

- Add <feature A> to Features section
- Add <column/row> to How It Compares table
- Move <feature B> from "What's coming" to Features (now shipping)
- Refresh <screenshot> to show new UI
- Bump version badge where applicable

No copy-edits to unrelated sections in this commit; README style edits
ride their own PR.
```

This constraint (one-purpose commits) keeps the README's history readable: every edit maps to exactly one release event.

---

## 3 · Repo branding — the GitHub page itself

GitHub's project page is a surface that almost no one else in fan-control space treats seriously. Treated well, it's a lightweight marketing surface that costs nothing.

### Elements to maintain

- **Repository description** (the one-liner at the top): currently undeclared. Should be: _"Automatic fan control for Linux. One binary, one URL. Cross-platform by v1.0."_ This changes once at v0.8 (drop "for Linux") and again at v1.0 (drop "Cross-platform by v1.0").
- **Topics** (tags on the repo sidebar): `fan-control`, `hardware-monitoring`, `linux`, `systemd`, `go`, `hwmon`, `daemon`. Add `windows` at v0.8, `macos` at v0.9. Add `ipmi`, `aio`, `liquid-cooling` as those backends ship.
- **Social preview image** (the card that shows when someone shares the repo link on Twitter/HN/etc): should be a clean rendering of the dashboard screenshot, generated once, updated only when the UI meaningfully changes. The default "random GitHub octocat" card is a missed opportunity every time someone shares the link.
- **README top banner**: badges already cover CI, release, Go version, license, platforms. Keep this stable; don't add vanity badges ("stars over time," "commits in last year") — they date badly and clutter the header.
- **Pinned issues**: use the pinned-issues feature for the hardware-compatibility meta-issue where users report what works / doesn't on their boards. One pinned issue, not three. Keeps new-contributor attention focused.
- **Discussions tab**: enable at v0.5 (when there's enough substance for users to have questions). Before that, GitHub Issues are the only channel and that's fine.

### Release page

Every release tag on GitHub produces a release page. Treat it as a mini-announcement:

- First paragraph: one-sentence "what's new" that a non-developer can read.
- Second block: bullet list of shipped features (same wording as the README "What's coming" entry that just migrated to Features).
- Third block: `CHANGELOG.md` excerpt (automatic via goreleaser).
- Fourth block: download links + checksums (automatic).
- Fifth block: "Known issues" if any.

Avoid emoji-headers and marketing copy. The release page is technical; save the pitch for the blog post.

### Screenshots

The dashboard screenshot in the README is load-bearing — it is how a browsing user decides in three seconds whether to read further. It needs to be:

- Taken on real hardware (not a VM with a fake fan map), so the readings look plausible.
- At 2x retina resolution, displayed at 720px width in the README.
- Updated when the UI changes in ways that would make the current screenshot look dated.
- Never include a machine's DMI strings, serial numbers, or anything personally identifying — redact first.

A second screenshot (first-boot setup page) demonstrates the zero-config flow and should be updated when the wizard changes shape.

---

## 4 · The "ventd.org" question

The project doesn't have a website. By v0.5 it probably should. Specifically:

- A landing page that acquires users who don't come from GitHub (blog commenters, forum lurkers, search-engine visitors who don't know what a GitHub repo is).
- A changelog / release archive indexed by search engines.
- A hardware compatibility matrix (same content as `docs/hardware.md` but auto-indexed).
- Eventually, a community showcase: "this is my ventd setup" type content.

**Not yet.** Doing this before v0.5 is premature:
- There's not enough content to fill a landing page beyond what the README already says.
- Maintaining a separate website forks the single-source-of-truth property of the GitHub repo.
- It's an ongoing cost (hosting, renewals, link rot, SEO) for zero current value.

**At v0.5:** stand up a static site generated from `docs/` content (Hugo, Zola, or similar). Simple, one person can maintain it, costs $10/year for DNS and $0 for hosting via GitHub Pages. The GitHub repo remains canonical; the site is a presentation layer.

**At v1.0:** the site becomes a real marketing surface. Screenshots per platform, testimonials from early users, a proper download CTA above the fold, the competitive comparison table that today lives in the README.

### Domain

`ventd.org` — check availability, register if available. `.org` signals non-commercial open source; `.io` signals commercial SaaS and is wrong for this project. `.dev` is also acceptable. Avoid `.com` unless there's an eventual plan to monetise.

---

## 5 · Cross-post checklist (on every minor release)

Used at each minor version bump. The rule: post only to communities where the release is genuinely relevant.

| Community | When relevant | Suggested post style |
|-----------|---------------|----------------------|
| `/r/linux` | Rarely — only for v0.x.0 ships that add a platform | Technical, focus on the single differentiator |
| `/r/homelab` | When IPMI or fleet features land | Practical: "Single-binary IPMI fan control" |
| `/r/buildapc` | When desktop-focused features land (AIO, acoustic) | Show the dashboard; link the screenshot |
| `/r/sffpc` | When quietness features land (MPC, dither) | Benchmark-driven — SFF folks care about real noise numbers |
| `/r/Framework` | When CROSEC lands | Framework-specific value prop |
| `/r/linuxhardware` | At v0.5+ when the profile DB is useful | Call for hardware reports |
| HN | Only v0.5 (maturity), v0.8 (Windows), v0.9 (Mac), v1.0 | Lead with the single strongest fact. No hype language. |
| Lobste.rs | Same as HN | Same |

**Never cross-post in the first 72 hours after a release** — bugs get reported in that window and responding to them is the priority over acquiring more users. Post to communities on day 4 or later when the release has settled.

**Never astroturf.** A single post in each relevant community per release, by the maintainer or a known contributor, with `[ventd]` or equivalent tag so people can filter. Not multiple accounts, not paid engagement.

---

## 6 · Failure modes to avoid

These are the traps the fan-control space has already walked into; ventd should not repeat them.

- **Overclaiming roadmaps in the README.** CoolerControl's README has listed features "coming soon" for multiple years that still haven't landed. This erodes trust. Ventd's protocol: features don't appear in the README Features section until they're in a shipped release.

- **Marketing-heavy screenshots.** Vendor tools ship with renders that don't look like the actual UI; users feel cheated when the real thing is plainer. Ventd screenshots are always real screenshots from real runs.

- **Hype language in release notes.** "Revolutionary," "the ultimate," "next-generation" — reviewers discount these phrases. Factual claims win. "Reduces peak fan RPM by 15% on a Ryzen 9 5950X under sustained 80W load" is a claim that gets repeated; "revolutionary thermal management" is a claim that gets mocked.

- **Promising timelines.** "v0.4 coming next month" — if it slips, credibility takes a hit. The roadmap is phases, not dates. Dates only appear in the release tag history, after the fact.

- **Community manager voice.** The project is written by engineers. The README and release notes should read like a serious engineer wrote them. Friendly is fine; performative is not. "Hey everyone! 🎉 Super excited to share..." is wrong for this project's positioning.

- **Logo before 1.0.** A custom logo is a v1.0 task. Before then, the GitHub default and the dashboard screenshot are the project's visual identity. Time spent on a logo before v1.0 is better spent on code.

- **Conference talks before v1.0.** The project isn't stable enough to be the subject of a conference talk. Contributing blog posts to niche publications is fine; booking a FOSDEM slot is not. Submit talks for the first FOSDEM after v1.0 ships.

---

## 7 · Immediate next steps

Given current state (post Phase 1 close, ultrareview running, v0.3.0 not yet tagged):

1. **Wait for ultrareview-1 to return clean.** If it returns blockers, fix them first.
2. **Tag v0.3.0.** Release notes pre-drafted at `release-notes/v0.3.0.md` (to be written).
3. **One-commit README update** migrates Phase 1 items from "What's coming" to Features (currently already reflected; this commit stamps the version badge).
4. **Set the repo description** to the one-liner above; add the `go`, `systemd`, `hwmon` topics if missing.
5. **Do not touch anything else.** Phase 2 dispatches resume after v0.3.0 is out.

The discipline is: one thing at a time. README right, release right, repo metadata right, then code. Don't start the Phase 2 queue while a release is mid-flight.

---

## 8 · Revisit cadence

This document is reviewed at every phase boundary (same cadence as `.cowork/STRATEGY.md`). If the strategic landscape shifts — FanControl.Releases goes open source, CoolerControl ships a zero-config wizard, Microsoft adds native fan control to Windows — this plan needs a rewrite that incorporates the new reality. Stale plans are worse than no plan.
