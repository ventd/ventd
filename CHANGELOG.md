# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
## [Unreleased]

### Added
- System class detection (`internal/sysclass`) — seven classes (NAS, MiniPC, Laptop, Server, HEDT-AIO, HEDT-Air, MidDesktop) with precedence chain, KV persistence, ambient sensor identification, AmbientBoundsOK gate, ServerProbeAllowed gate, and laptop EC handshake (spec-v0_5_3 PR-A)
- Idle predicate foundation (`internal/idle`) — PSI-primary/loadavg-fallback, durability-gated StartupGate, hard battery/container refusals, process blocklist with SetExtraBlocklist, RuntimeCheck baseline-delta, exponential backoff with daily cap (spec-v0_5_3 PR-A)
- AppArmor: add /proc/pressure/*, /proc/mdstat, /proc/loadavg, /proc/uptime, /proc/spl/kstat/zfs/**, /sys/fs/btrfs/**, /proc/sys/kernel/osrelease

## [v0.5.2] - 2026-04-27

### Added
- Cross-backend polarity probe + wizard screen (spec-v0_5_2) (#673)## [v0.5.1] - 2026-04-27

### Added
- Install design system foundation and new landing page (#661)- Schema v1.2 — typed experimental block with Levenshtein forward-compat (#662)- Framework scaffolding (spec-15 PR 1) (#663)- Amd_overdrive F1 — precondition, HAL gate, RDNA4 kernel check (#664)- Persistent state foundation + smart-mode README (spec-16 PR 1) (#669)- Catalog-less hardware probe and three-state wizard fork (spec-v0_5_1) (#670)
### Chore
- Ignore worktrees, log spec-12 PR 1 cost calibration (.41)- Ignore worktrees, log spec-15 PR 1 cost calibration ($9.78)- Remove leaked CC prompt files (#671)
### Documentation
- Spec-14a + spec-14b — hardware-profiles repo design + submission flow (#658)- Add v0.6.0 mockup screenshots and HTML preview (#665)- Update screenshots to v0.6.0 UI and add page gallery (#666)- Add spec-16, smart-mode, v0.5.1 probe, and amendment specs (#667)- Remove superseded spec-04 pi-autotune and amendment (#668)
### Fixed
- Wire os.DirFS defaults for SysFS/ProcFS/RootFS in New() (#672)## [v0.5.0] - 2026-04-26

### Added
- Freeze v1 schema (spec-03 PR 1) (#629)- Add CC tooling bundle (preflight, release-validate, templates) (#632)- Spec-03 PR 2a — three-tier matcher and chip-family catalog (#635)- Spec-03 PR 2b — runtime probe + unified types + apply-path enforcement (#636)- Spec-03 PR 2c - diagnostic bundle, redactor, NDJSON substrate (#639)- Spec-03 PR 2d — GPU vendor catalog (NVIDIA/AMD/Intel) (#643)- Spec-03 PR 3 — board catalog seed (15 entries) (#644)- Spec-03 PR 4 - schema v1.1 (bios_version, dt_fingerprint, unsupported) (#646)- Spec-03 PR 5 — scope-C catalog seed + spec-12/13 docs (#647)- Capture pipeline — pending profiles after calibration (spec-03) (#649)
### Chore
- Add triage-run.sh + verify-local.sh (#628)- Post-spec-03 PR 4/5/gaps/capture cleanup (#653)
### Documentation
- Add spec-03 PR 1 working document- Log PR #632 cost + spend velocity tracking (#634)- V0.5.0 spec batch + GPU catalog research (#641)- Predictive thermal thesis + v0.4.0 Corsair / v0.3.1 IPMI shipped (#642)- PR 4/5 gaps - hwdb-schema v1.1 section, amendment status, changelog (#648)- V0.5.0 hardware database, runtime probe, ventd diag (#654)## [v0.4.1] - 2026-04-25

### Added
- Rulelint pattern for CI action pinning (spec-08) (#621)- AppArmor profiles + HIL validation (spec-06 PR 2) (#627)
### Documentation
- Add v0.4.1 backlog specs 06-08 (#618)- Reflect v0.3.1 IPMI ship + v0.4.0 Corsair ship (#620)
### Fixed
- Install-contract drift fixes (spec-06 PR 1) (#625)## [v0.4.0] - 2026-04-24

### Added
- USB HID primitives + fakehid fixture (#591)- Pure-Go hidraw substrate, drop go-hid (#593)- Commander Core / Core XT / ST backend (#608)- Udev uaccess rule for Corsair + architectural guardrail tests (#611)- PR 4 — VID gate + docs + CHANGELOG for v0.4.0 (#615)
### Documentation
- Add hidraw substrate spec + amendment + rules (#592)- Add hwmon research reference- Record deferred hwmon gotchas from §17 gap analysis- Liquid-safety.md invariants for Corsair backend (#596)- Add RULE-LIQUID-07 for kernel driver yield (#601)- Seed TESTING.md with hardware and VM inventory (#610)- Consolidate TESTING.md at repo root (#612)- Add manual-file-placement duplicate check (#613)- Add spec-05 predictive thermal shell + amendments (#614)## [v0.3.1] - 2026-04-23

### Added
- Apple Silicon detection + hwmon wrapping (P2-ASAHI-01) (#279)- ARM SBC sysfs PWM backend (P2-PWMSYS-01) (#277)- Shared USB HID primitive layer (P2-USB-BASE) (#281)- Framework + Chromebook EC backend (P2-CROSEC-01) (#282)- Native IPMI backend via /dev/ipmi0 ioctl (P2-IPMI-01) (#285)- PI curve with anti-windup + per-channel state (P4-PI-01) (#315)- Drew-audit.yml workflow for post-tag audit gates (#354)- Recognise // regresses #N and // covers #N magic-comment annotations (closes #330) (#352)- Per-session git worktree isolation for CC spawns (#396)- Auto-label PRs by changed paths and title prefix (#407)- Daily stale role:atlas escalation workflow (#408)- Allowlist-scope guard for CC task PRs (#409)- Audit-signal workflow — escalate silent Cassidy audits (IMPROV-F) (#414)- Spawn_cc_inline, wait_for_session, spawn_cc_batch (#423)- Automated drift detection replacing ultrareview (#425)- Add github-mcp.service for ci-log-tools build deployment (#428)- Ops-mcp — scoped host operations MCP server (#429)- Fuzz-smoke.yml + fuzz-long.yml scaffolding for TX-FUZZ-CORPUS-GROW (#381)- Pre-release-check.yml pre-tag validation workflow (RELEASE-CHECK-01, closes #328)- CycloneDX + SPDX SBOMs on every release (P10-SBOM-01)- Cowork-query CLI for events.jsonl (#426)- Release-time git-cliff CHANGELOG automation (closes #385) (#450)- Cosign keyless + SLSA L3 provenance (P10-SIGN-01) (#447)- Pve-template-prep.yml workflow for Arch/Void/Alpine templates (#395)- Reproducible builds + rebuild-and-diff verification (P10-REPRO-01) (#516)- Injection seam for fixture-driven tests (#531) (#540)- Add 18 new tools, fix INSTALL.md paths, logrotate, sudoers expansion (#565)- Ship logrotate fragment for audit log (#562) (#575)- Add filter/session/guardrail hooks- Add ventd-rulelint skill (skill-creator evaled)- Add ventd-specs skill (skill-creator evaled)- Privilege-separated sidecar (spec-01 PR 2) (#588)- DMI gate + vendor profiles + v0.3.1 changelog (#590)
### CI
- Replace per-PR CHANGELOG edits with release-time git-cliff (#410)
### Changed
- Extract shared Base struct (closes #271) (#314)
### Chore
- Add PR template with Risk class + Verification + Concerns + Deviations schema (per Cassidy D) (#346)- SHA-pin actions, digest-pin containers, hash-verify Alpine Go (SUPPLY-CI-HYGIENE-01) (#355)- Activate drew-audit Gates 2/3/4 (SBOM + cosign + repro) (#523)- Gitignore .mcp.json at repo root- Enforce LF line endings (WSL2 CRLF workaround)- Track hooks, skills, and settings; ignore only local- Remove spec-writer (superseded by ventd-specs) and eval workspaces
### Documentation
- Drop model-mismatch abort rule- HTTP API reference (closes #269) (#309)- GHSA policy + v0.3.0 stdlib CVE advisory draft (SUPPLY-GHSA-01)- Promote Safety to named section; reorder Install to lead with inspect-first (#567)- Correct infrastructure inventory for solo-dev workflow (#580)- Diet CLAUDE.md to 67 lines; move infra to docs/- Commit masterplan + test masterplan to repo- Bind ipmi-safety invariants to subtests- Split PR 2 into 2a+2b to fix sidecar spec gap- Bind legacy orphan subtests to invariant rules (#586)- Revise PR split for privilege-separation sidecar (#587)
### Fixed
- Scheduler↔manual-override race (fixes #289 concern 1) (#294)- Guard read_no_mutation type assertion with fileBacked check (closes #266) (#295)- Reject sensor/fan name collisions (closes #293) (#319)- Fsync tmp file and parent dir in mergeModuleLoadFile (closes #311) (#336)- Reset failure counter after Restore to stop log spam (closes #306) (#338)- Close-checks + mu-serialise per-handle I/O (closes #305 concerns 1-2) (#340)- ErrNotPermitted sentinel restores fatal-on-permission for manual-mode (closes #288) (#341)- Error on non-zero Restore cc + Zone=0 fallback + response bounds check (closes #307) (#361)- Verified-first match pass so unverified profiles can't shadow verified ones (closes #308) (#362)- Split peek/redirect timeouts in tlsSniffListener + escape redirect body against XSS (#364)- Plug CAUGHT #7 durability gaps in calibrate/config/selfsigned (closes #376 #378 #365) (#412)- Surface parseIndex errors with log (closes #380)- Persist setup token to /var/lib/ventd (survives reboot) (#383)- AppArmor profile blocks cert-gen and hwmon reads on fresh Ubuntu (#459) (#462)- Correct setup-token recovery instructions and rotation (#458) (#464)- Replace self-restart with in-process config reload (#466) (#478)- Add ventd user to NVIDIA device group + actionable NVML diagnostic (#461) (#465)- Persist admin credentials to auth.json, protect from config-save overwrite (#463) (#481)- Reject 0xFFFF sentinel reads to prevent fan-to-max on invalid sensor (#460) (#469)- Add authPath param to runDaemonInternal signature — unbreak main- Reject module names with whitespace in sensors-detect parser (#489)- Collapse curve editor form to single column on mobile (#488) (#491)- Install ventd-recover binary + OnFailure hook (#484) (#494)- Atomic-write with unique tmp path, fix ubuntu-arm64 abort-persists race (#467) (#492)- Curve editor form hydration + PATCH semantics (#483) (#493)- Heuristic sensor binding + accurate error messages for mini PC / idle hardware (#504) (#521)- Store acquired-path after write success, not before (closes #348) (#528)- VENTD_STATE_DIR env override for scratch-sysroot test harness (#532)- AppArmor profile v2 + systemd directory modes (closes #498) (#527)- Apply retry+RestoreOne to manual-mode write path via writeWithRetry helper (closes #272) (#357)- Unique tmp suffix to eliminate concurrent-Save race (#515) (#538)- Wizard state cache TTL + server re-validation on load (#502) (#542)- Immediate RestoreOne on sentinel first-tick when !hasLastPWM (#512) (#539)- Suppress sentinel values at status/SSE serialization boundary — v2 (#460) (#522)- Replace gate2 cliff.toml tautology with cc-count check (#563)- Remove streamable_http_path="/" so /mcp endpoint works (#568)- Sync pwmsys_test.go with new fakepwmsys fixture API (closes #552) (#572)- Gate slider value updates on drag-active state to prevent mid-gesture jumps (#507) (#570)- Update github-mcp Docker tag from ci-log-tools to mcp-t12 (#561) (#574)
### Rules
- Bind hwmon-safety.md invariants to RULE- headings (closes #313) (#337)- Bind calibration safety invariants to rulelint RULE- headings (closes #235) (#363)
### Tests
- Add registry unit tests (closes #267, ultrareview-1) (#276)- Extend RULE-WD-RESTORE-EXIT subtest with RestoreOne leg (fixes #287) (#299)- Extend RULE-WD-RESTORE-EXIT subtest with RestoreOne leg (fixes #287) (#300)- Add ErrNotPermittedFatal_ManualMode regression test (closes #347) (#353)- E2e coverage for panic button and profile switching (#427)- Add TestRegression_Issue86_HwmonRenumberColdBoot (closes #86 from regression backfill) (#439)- Add TestRegression_Issue59_TLSConfigMigration (closes #59 from regression backfill) (#437)- Add TestRegression_Issue103_HwmonStartupRetry (closes #103 from regression backfill) (#438)- Fakeipmi fixture — Options, canned responses, BusyCount (T-IPMI-01a) (#530)- First-boot → configured reload starts controllers (#514) (#545)- Fakedmi fixture — DMI sysfs tree (T0-INFRA-fakedmi) (#550)- Fakepwmsys fixture (T0-INFRA-fakepwmsys)- 22-entry fingerprint match suite + determinism property (T-FP-01) (#571)- Add fakeipmi fixture scaffolding- Add IPMI safety invariant tests (T-IPMI-01)- T-IPMI-02 sidecar privilege-boundary verification (#589)
### Build
- Commit binary-size baseline + drift check (#453) (#537)
### Dead
- Prune 6 unreachable functions (closes #268, ultrareview-1) (#278)## [v0.3.0] - 2026-04-18

### Added
- Group dashboard by category with collapsible sections on mobile (#130)- Gate action=added rebind behind hwmon.dynamic_rebind, drop udev-settle ordering (#125)- Version metadata, health probes, /api/v1 alias (#155)- Reject min_pwm:0 without allow_stop:true at load (#158)- Empty-state copy for every dashboard section (#185)- Visual binding + clickable mix sources (#190)- Apply diff modal + /api/config/dryrun (#195)- Populated Settings modal + system status endpoints (#197)- Rescan hardware button + /api/debug/hwmon telemetry (Session C 2h) (#209)- Panic button + profiles (Session C 2e) (#212)- Wizard chip-name explainer during calibration (Session D 3g) (#220)- Hysteresis + smoothing per curve (Session D 3a) (#221)- Multi-point curves with interactive editor (Session D 3b) (#222)- Curve simulation preview with live projections (Session D 3d) (#224)- Migrate PWM 0-255 to percentage 0-100 (Session D 3f) (#226)- Proxmox-driven cross-distro install harness (#160)- Time-series sparklines on dashboard cards (#223)- Scheduled profile switching (#225)- Persist security-module load outcome + startup WARN (#232)- Surface setup URL + token in a visually distinct block (#231)- Redirect HTTP-&gt;HTTPS on the TLS port via first-byte sniff (#233)- Fingerprint-keyed hwdb (#246)- Baseline settings.json for non-interactive CC sessions (#250)- Permissions-Policy + ETag on embedded UI (P10-PERMPOL-01) (#253)- Opt-in remote refresh with SHA-256 pin (#257)- Symmetric retry+RestoreOne on PWM write failure (P1-HOT-02) (#263)
### CI
- Expand matrix to Ubuntu 24.04, Fedora 41, Arch, Alpine (#114)- Stable matrix names for ruleset alignment (#159)- AppArmor profile smoke check + arm64 matrix row (#219)- Add runner-smoke workflow for self-hosted HIL verification (#249)- Regression-test-per-closed-bug lint (T0-META-02) (#254)
### Changed
- Extract sysfs/procfs roots into Manager for testability (#163)- FanBackend interface (#247)- Drive via hal.FanBackend (P1-HAL-02) (#262)
### Chore
- Bump actions/cache from 4 to 5 (#83)- Add CC issue-logger shell library (#137)- Issue and pull-request templates (#150)- Makefile + fix feature.yml label and PR template refs (#154)- Gofmt sweep + CI gate (#156)- Delete empty t.Run pass-through stubs left by #163 (#175)- Track .claude/rules/*.md in version control (#218)- Ignore .cowork/ outside the cowork/state branch (#236)- Event-sourced state + dashboard (#248)- Bump Go toolchain to go1.25.9 (closes 17 stdlib CVEs) (#270)
### Documentation
- Add v0.3.0 plan (#110)- Correct reboot-survival gate reference to #111 (#113)- Mark v0.3.0 CI-matrix gate as landed in #114 (#117)- Expand v0.3 plan and draft v0.4 plan (#120)- Record v0.3 controller-safety fixes in CHANGELOG, COVERAGE, v0.3 plan (#127)- Record Batch 2 fixes and API metadata work (#157)- Record setup.Manager root extraction (#163) (#166)- Cross-link rig reboot-PASS tracker #167 (#169)- Refresh internal/setup after #163 lands (#170)- Document non-linux contributor workflow (#205)- CHANGELOG narrative for Phase 2 UI + refresh stale systemd unit comment (#213)- Add public roadmap (#237)- Add hardware-report issue template (#238)- Add regression-test checkbox to PR template (T0-META-03) (#240)- Add CLAUDE.md priming Claude Code for Cowork task activation (#243)- Correct feature list to shipped capability; add roadmap section (#264)- Finalize v0.3.0 release notes
### Fixed
- Allow ssh + web UI in ufw-incus.rules (#108)- Allow the installer's PWM-holder preflight to pass when ventd is already running (#107) (#109)- Rebind controllers on action=added topology change (#86 Proposal 3) (#112)- Close allow_stop + ctx-cancel exit-path safety gaps (#124)- Attach profile on both /usr/bin and /usr/local/bin (#134)- Initialise collection fields in Empty/Default (#151)- Nct6683 module + sensors-detect Driver variant (#153)- Race_count subshell and default label set (#152)- Reboot-survival verifier as a shipping artifact (#111) (#164)- Single source of truth for systemd unit and udev rules (#161)- HEALTHCHECK probes both HTTP and HTTPS to survive wizard-triggered TLS activation (#176)- Declare color-scheme so browser dark-mode overrides don't fight the theme toggle (#203)- HTTPS URL on re-install + CRLF self-heal + LF pin (#207)- Profile dropdown fires `change`, not `click` (#214)- Stabilise TestE2E_SettingsModal_PopulatedSections wait gate (#217)- HandleSystemReboot refuses 409 when running in a container (#230)- Add relative-path Write allowlist for repo dirs (#256)- Append-not-overwrite in persistModule (#261)
### Performance
- Drop modinfo shellouts, parse modules.alias directly (P1-MOD-01) (#259)- Eliminate per-tick allocations (#260)
### Security
- Add Dockerfile, compose, and device-passthrough docs (#142)
### T0-INFRA-03
- Faketime fixture — Clock + goroutine-safe timers (#245)
### T0-META-01
- Rule-to-subtest binding lint (#244)
### Tests
- Bind hwmon-safety invariants to table-driven cases (#118)- Bind wizard orchestration invariants to table-driven cases (#136)- Cover hwmon scan path via injected sysfs root (#138)- Cover autoload parsers and driver-need heuristics (#139)- Setup wizard state machine + reboot handler invariants (#178)- Diagnostic suite + one-command runner + Claude Code workflows (#227)- Add fixture library skeleton (#239)- Implement fakehwmon fixture (T0-INFRA-02) (#241)- Bind watchdog safety invariants (T-WD-01) (#255)- T-HAL-01 — lock backend contract invariants (#258)
### Deploy
- Fix apparmor profile parse failure on Debian 13 (#204)
### Docker
- VENTD_GID build-arg + image build CI (#162)
### Packaging
- Add AUR PKGBUILDs for ventd-bin and ventd (#119)- Add NixOS flake and module (#145)
### Validation
- Spare-VM UFW dry-run harness (#99)## [v0.2.0] - 2026-04-15

### Added
- Lucide icon sprite replaces unicode glyphs (#48)- Server-sent events for live fan state, keep polling fallback (#47)- Phase 4.5 foundation — breakpoints, drawer sidebar, header reflow (#57)- Phase 4.5 PR 2 — card grid reflow and mobile card internals (#69)- Phase 4.5 PR 3 — touch-aware curve editor (#89)- Phase 4.5 PR 4 — 44x44 touch targets + responsive modals (#92)
### CI
- Add govulncheck workflow (#14)- Resolve checkout@v6 / govulncheck-action auth conflict (#41)
### Changed
- Split styles/app.css into tokens / base / layout / components- Split ui/scripts/app.js into 5 defer-loaded modules
### Chore
- Surface ParseUint errors and replace Fprintf with slog (#11)- Wrap stdlib errors across internal/* (#16)- Bump actions/setup-go from 5 to 6 (#23)- Bump goreleaser/goreleaser-action from 6 to 7 (#22)- Gitignore AGENTS.md (#29)- Finalize v0.2.0 entry (#46)- Un-draft v0.2.0 after rig PASS (#80) (#87)
### Documentation
- Install-smoke notes for alpine (pass) and void (blocked)- Coverage snapshot (#17)- Correctness sweep across README and docs/ (#19)- NVIDIA GPU fan control setup guide for v0.2.0 (#34)- Add first-boot setup screenshot (#53)- Add dashboard screenshot (#77)- Swap dashboard.png placeholder for real screenshot (#62, #77) (#79)- Mark v0.2.0 release blocked pending cold-boot first-boot-mode fix (#104)- Un-draft v0.2.0 after cold-boot re-verify (9/9 PASS) (#106)
### Fixed
- Surface ParseUint errors in nvidia sensor read (#13)- Phase 0 — unblock CSP, first-boot probe, lockout UI (#26)- Phase 0.5 — drop CSP 'unsafe-inline' from script-src and style-src- Resolve hwmon symlinks in buildChipMap (#31)- Disambiguate multi-match chip_name via hwmon_device (#42)- Ensure /etc/ventd config ownership is ventd:ventd (#38)- Pre-validate generated config before the review screen (#32)- Gate case_curve on len(gpuFans) > 0 (#33)- Move OnFailure= to [Unit] so recovery actually fires (#66)- Honour hwmon_device= on single-candidate resolver match (#86)- Order ventd after systemd-udev-settle so hwmon chips enumerate first (refs #86) (#101)- Enforce 44×44 touch targets on slider thumb, checkbox, and edit-icon (refs #68) (#102)- Don't mistake a hwmon udev race for a missing config (#103) (#105)
### Security
- Security PR 1 — login rate limit, CSRF, cookies, body caps, setup token (#4)- Refuse to start when configured TLS cert/key are missing (#9)- Run ventd as User=ventd (#12)- V0.2.0 notes (#52)
### Tests
- Pure-logic tests for curve and calibrate (#18)- Decouple TestLoad_NoChipNameNoOpsBackwardCompat from host /sys (#49)- Decouple TestCheckResolvable_EmptyChipNameIgnored from host /sys (#63)
### Config
- ResolveHwmonPaths for chip-name-based re-anchoring (#20)- Auto-populate TLS paths from /etc/ventd/tls.{crt,key} on load (#71)
### Deploy
- Security PR 2 — systemd unit hardening (#6)- Own /etc/ventd via ConfigurationDirectory (#10)- Zero-terminal install + sandbox-correct module loading + ChipName auto-resolve (#21)- Make the README's safety promises actually true (#25)
### Hwmon
- Quiet module-persist warning under sandbox (#15)
### Install
- Preflight conflict checks for fan daemons and port 9999 (#8)- Refresh systemd units on every run so upgrades pick up unit changes (#65)
### Release-notes
- Add v0.2.0-readiness tracker for overnight ops (#64)- Audit gaps vs merged PR list (#67)- Update for #58/#60 closures (#72)- Record post-#70 VM matrix + goreleaser re-verify (#75)- Record #59 closure and PR #74 harness update (#78)- Add upgrade-path notes for #65 and #71 (#81)- Watchdog recovery highlight + readiness final state (#85)- Final handoff — touch-aware curve editor + CHANGELOG marked done (#91)
### Safety
- Refuse plaintext LAN binds; auto-gen TLS on first boot; XFF trust model (#5)
### Setup
- Reject wizard configs the resolver would refuse on next boot (#56)- One-click module remediation for missing hwmon chips (#73)
### Shutdown
- Integration test + goleak regression guard (#7)
### Style
- Switch body to system sans, keep mono for numeric readouts (#39)
### Validation
- Fresh-VM install smoke harness for v0.2.0 (#45)- Phoenix-MS-7D25 v0.2.0 final runbook (PR-F3.0) (#44)- V0.2.0 fresh-VM smoke — all four distros PASS (#50)- V0.2.0 final rig re-verify on phoenix-MS-7D25 (FAIL) (#54)- Proxmox-driven fresh-VM install smoke harness (#51)- V0.2.0 post-unit-fix cross-distro re-verify (#70)- --refresh-images flag + corrupted-cache recovery docs (#74)- Fix 0a.i/0a.iii false positives on real-world configs (#76)- V0.2.0 final rig re-verify on phoenix-MS-7D25 (PASS) (#80)- Upgrade-path smoke for TLS config migration (#82)- UFW rules for Incus bridge + enable procedure (#88)- Assert resolved pwm_path lives under configured hwmon_device (refs #86) (#100)
### Wip
- Phase 0.5 — utility CSS classes + index.html data-action conversion- Phase 0.5 group 1 — sensor cards data-action conversion- Phase 0.5 group 2 — fan cards data-action conversion- Phase 0.5 group 3 — curve cards data-action conversion- Phase 0.5 group 4 — curve editor data-action conversion- Phase 0.5 group 5 — header, sidebar, hw device toggle- Phase 0.5 group 6 — modals, setup wizard, last inline styles- PR-B0.5 checkpoint — state.js + api.js drafted, not yet wired## [v0.1.1] - 2026-04-14

### Documentation
- Add Tier 0.3 and install-smoke validation notes
### Install
- Use portable sha256sum check for busybox compat
### Release
- Dual-variant builds and musl-compatible installer
### Safety
- Shutdown-clean sessions, panic-safe watchdog, mode-guarded PWM- Consolidate JSON encoder logging and drain errCh on shutdown (#3)## [v0.1.0] - 2026-04-14

### Hwmon
- Drop ineffectual default in watcher.promote
### Install
- Auto-start the service and shrink post-install output
### Release
- Auto-publish release artifacts (drop draft gate)
### Scripts
- Make install.sh pipe-installable with release download + checksum verify