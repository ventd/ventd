# Spec 06 — Install-contract closure for v0.4.1

**Masterplan IDs this covers:** Closes install-contract drift uncovered in v0.4.0 Proxmox smoke test (2026-04-25). Binds install-time invariants to the same `.claude/rules/*.md` pattern used for runtime safety.
**Target release:** v0.4.1 (point release — fix-only, no new features).
**Estimated session cost:** Sonnet, 2 sessions, $10–15 total. No Opus required. HIL required for AppArmor profile PR only.
**Dependencies already green:** v0.4.0 shipped (`ventd/ventd@v0.4.0`). `cmd/ventd-ipmi` sidecar + sysusers.d/tmpfiles.d pattern from spec-01 PR 2a.

---

## Why this ships v0.4.1

Four independent drift bugs found during Proxmox smoke test on `fc-test-ubuntu-2404` (VM 207) immediately after v0.4.0 tag:

1. **`User=ventd` referenced in `deploy/ventd.service` but never auto-created.** systemd fails with `217/USER`. Main daemon cannot start on a fresh install without manual `useradd`.
2. **`AppArmorProfile=ventd` references a profile that is not shipped.** systemd fails with `231/APPARMOR` on AppArmor-enforcing distros (Ubuntu, Debian).
3. **`deploy/ventd.service` has `OnFailure=ventd-recover.service` but the recover unit is not in `deploy/`.** systemd logs a cascading failure chain with no recovery path.
4. **`web.listen` default `0.0.0.0:9999` trips the first-boot TLS safety check.** Daemon exits on first run of a fresh install until the user edits config — violates the zero-config promise.

Each is a *contract drift*: the unit file promises something the install artefact does not deliver. v0.4.0 shipped because validation only ran on the dev box that had these things pre-existing from earlier versions. Fresh Proxmox VM exposed them all within 15 minutes.

**These are the exact failure mode the README promises against.** ventd's target audience is TrueNAS/Unraid/Proxmox homelab users who install from a release tarball. Four drift bugs in the install path is a credibility hole in the minor release the `--list-fans` feature was meant to sell.

## Scope — what this session produces

Two PRs, sequential. PR 1 fixes three pure-code drift bugs + adds the rule file. PR 2 ships the AppArmor profile + HIL validation.

### PR 1 — Install-contract invariants + three drift fixes

**Files (new):**
- `.claude/rules/install-contract.md` — 4 rules (3 active + 1 stub for AppArmor, filled in PR 2)
- `deploy/sysusers.d-ventd.conf` — sysusers.d drop-in for `ventd` user (mirrors `sysusers.d-ventd-ipmi.conf` from spec-01 PR 2a)
- `deploy/ventd-recover.service` — the missing unit referenced by `OnFailure=`
- `deploy/install-contract_test.go` — unit-file parser + 4 subtests (ini parser pattern from spec-01 PR 3, no external deps)

**Files (modified):**
- `deploy/ventd.service` — no behaviour change; tests assert the existing references now resolve
- `internal/config/defaults.go` (or wherever `web.listen` default lives — CC will grep) — change default from `0.0.0.0:9999` to `127.0.0.1:9999`
- `internal/config/config_test.go` — add subtest for new default + TLS-gate interaction
- `deploy/README.md` — install-step update listing sysusers.d drop-in and ventd-recover.service
- `CHANGELOG.md` — v0.4.1 entry under `[Unreleased]`

**Files (deferred to PR 2):**
- `deploy/apparmor.d/ventd` — real profile, ships with HIL validation

**Invariant bindings (`.claude/rules/install-contract.md`):**

1. `RULE-INSTALL-01` — Every `User=` directive in `deploy/*.service` MUST have a matching entry in `deploy/sysusers.d-*.conf`. Parser fails the build if a unit references a user not declared by a shipped sysusers drop-in. **Binds to:** `TestInstallContract_UserDeclared`.

2. `RULE-INSTALL-02` — Every `OnFailure=` directive MUST reference a unit file that exists in `deploy/`. Cascading failure chains are not allowed to terminate in a missing unit. **Binds to:** `TestInstallContract_OnFailureResolves`.

3. `RULE-INSTALL-03` — The default value of `web.listen` in shipped config MUST NOT bind to `0.0.0.0` without TLS configured. Localhost-only default (`127.0.0.1`) is acceptable; external-binding default requires TLS. **Binds to:** `TestInstallContract_WebListenDefault`.

4. `RULE-INSTALL-04` — (stub in PR 1, active in PR 2) Every `AppArmorProfile=` directive MUST reference a profile file shipped in `deploy/apparmor.d/`. **Binds to:** `TestInstallContract_AppArmorProfileShipped` (marked `t.Skip("filled in PR 2")` until PR 2 lands).

**Why localhost default (not dropping the web listener entirely):** ventd's web UI is opt-in at runtime but enabled by default in `ventd.yaml.example`. Dropping it defaults-off would be a user-visible regression. Localhost-only keeps the feature working for `ssh -L` and reverse-proxy setups without tripping the TLS gate on fresh boot.

**Test parser requirements (same constraints as spec-01 PR 3 TestSidecarUnit_MinimalPrivilege):**
- No external deps (no go-ini, no yaml.v3 beyond what's already in go.mod).
- Hand-rolled ini parser stays under 80 lines.
- Test data: parse actual `deploy/*.service` files, not fixtures.
- Reading the parser implementation from spec-01 PR 3 is explicitly encouraged — mirror it.

### PR 2 — Real AppArmor profile + HIL validation

**Files (new):**
- `deploy/apparmor.d/ventd` — AppArmor profile for main daemon
- `deploy/apparmor.d/ventd-ipmi` — AppArmor profile for sidecar (covers spec-01 PR 2a TODO)
- `docs/apparmor.md` — profile reference, complain-mode debugging guide
- `validation/vm-apparmor-smoke.sh` — HIL script running profile under enforce on fresh Ubuntu + Debian VM

**Files (modified):**
- `.claude/rules/install-contract.md` — RULE-INSTALL-04 flipped from stub to active
- `deploy/install-contract_test.go` — `TestInstallContract_AppArmorProfileShipped` flipped from skip to active
- `deploy/README.md` — AppArmor section added, complain-mode toggle documented
- `deploy/ventd-ipmi.service` — confirm `AppArmorProfile=ventd-ipmi` points to shipped profile (removes spec-01 PR 2a's TODO)
- `CHANGELOG.md` — v0.4.1 entry extended

**Profile scope (deploy/apparmor.d/ventd):**
- Read-only access to `/sys/class/hwmon/**`, `/sys/class/dmi/id/*`, `/sys/class/thermal/**`, `/proc/cpuinfo`, `/proc/modules`.
- Read-write access to `/sys/class/hwmon/*/pwm*` and `/sys/class/hwmon/*/pwm*_enable` (the only write path ventd needs for hwmon control).
- Read-only access to `/etc/ventd/**`, `/var/lib/ventd/**`.
- Network: `unix` (for IPMI sidecar socket), `inet tcp` bound to listen address only.
- Explicit deny: `/dev/ipmi*` (sidecar-only), `/dev/mem`, `/dev/kmem`, `/sys/kernel/**`, capability network_admin, capability sys_admin.
- Capabilities allowed: none. Main daemon runs capability-less per spec-01 RULE-IPMI-<main-zero-caps>.

**Profile scope (deploy/apparmor.d/ventd-ipmi):**
- All of main-daemon scope PLUS:
- Read-write access to `/dev/ipmi0`, `/dev/ipmi1`, `/dev/ipmidev/0`.
- Capability `sys_rawio`.
- Unix socket bind on `/run/ventd/ipmi.sock` only.

**HIL validation approach:**
- `validation/vm-apparmor-smoke.sh` runs on Proxmox VMs you'll stand up via CC automation.
- Script: boot VM, install ventd from local tarball, apply profile in enforce mode, run `ventd --probe-modules --dry-run` + `systemctl start ventd`, verify no AppArmor denies in `/var/log/audit/audit.log`.
- Runs against: Ubuntu 24.04, Debian 13. (Fedora + Arch default to SELinux/no-LSM; profile is a no-op — HIL scope covers AppArmor-enforcing distros only.)
- Output: committed log at `validation/apparmor-smoke-<distro>-<date>.md`, green line required for v0.4.1 tag.

**Complain-mode documentation (docs/apparmor.md):**
- Explain enforce vs complain in two paragraphs.
- Command: `sudo aa-complain /etc/apparmor.d/ventd` to debug.
- Command: `sudo aa-enforce /etc/apparmor.d/ventd` to restore.
- Issue template update: audit.log traces welcome in bug reports.
- **Not a code change** — pure docs addition.

## Definition of done

### PR 1
- [ ] `go test -race ./deploy/...` passes.
- [ ] All 4 install-contract tests have subtests (RULE-INSTALL-04 in skip state).
- [ ] `ventd.service` + `ventd-ipmi.service` parse cleanly under `systemd-analyze verify`.
- [ ] `deploy/sysusers.d-ventd.conf` creates `ventd` user on `systemd-sysusers deploy/sysusers.d-ventd.conf` dry-run.
- [ ] `deploy/ventd-recover.service` parses cleanly under `systemd-analyze verify`.
- [ ] `web.listen` default change documented in CHANGELOG with migration note.
- [ ] `tools/rulelint` passes — no orphan rules, no orphan subtests.
- [ ] Fresh Proxmox VM smoke test: install → systemctl start ventd → running (no 217/USER, no missing-recover-unit log).

### PR 2
- [ ] Both AppArmor profiles parse cleanly under `apparmor_parser -Q -T`.
- [ ] `validation/vm-apparmor-smoke.sh` green on Ubuntu 24.04 + Debian 13.
- [ ] `TestInstallContract_AppArmorProfileShipped` passes (no longer skipped).
- [ ] `docs/apparmor.md` present with enforce/complain toggle instructions.
- [ ] spec-01 PR 2a AppArmor TODO removed from `deploy/README.md`.
- [ ] v0.4.1 tag cut only after both HIL logs are committed to `validation/`.

## Explicit non-goals

- No SELinux policy. Fedora/RHEL/Arch use SELinux or no-LSM; their `AppArmorProfile=` line is ignored by systemd on those systems. Real SELinux policy is a separate spec (post-v1.0).
- No Polkit rules. Unit hardening covers what polkit would gate.
- No snap/flatpak/appimage packaging. Tarball + systemd units only for v0.4.1.
- No changes to `cmd/ventd-ipmi` sidecar behaviour. AppArmor profile wraps existing binary.
- No new config fields. `web.listen` default changes but schema is unchanged.

## Red flags — stop and page me

- CC wants to "improve" the AppArmor profile beyond the scope listed → scope is the list above; tighter rules go in v0.5.0 after broader HIL.
- CC proposes dropping `web.listen` default to `""` (disabled) → user-visible regression, not allowed; localhost-only is the decision.
- CC suggests adding Polkit integration "while we're here" → separate spec.
- HIL script fails on Ubuntu 24.04 → profile needs tightening; do NOT ship a complain-mode-only profile for v0.4.1 enforce target.
- CC wants to author SELinux policy because Fedora fails → Fedora doesn't fail, `AppArmorProfile=` is silently ignored; verify with `systemctl status ventd` on Fedora VM before rabbit-holing.

## CC session prompt — copy/paste this

```
Read /home/claude/specs/spec-06-install-contract.md end to end. Then read:
- specs/spec-01-ipmi-polish.md (ini parser pattern, sysusers.d pattern)
- cc-prompt-spec01-pr3.md (TestSidecarUnit_MinimalPrivilege parser — mirror it)
- deploy/ventd.service (current drift targets)
- deploy/ventd-ipmi.service (sidecar-pattern reference)
- deploy/sysusers.d-ventd-ipmi.conf (if it exists; mirror for ventd user)

Start with PR 1 only. Do not start PR 2 in the same session — PR 2 needs HIL
on Proxmox VMs I will stand up separately.

PR 1 scope:
1. Create .claude/rules/install-contract.md with 4 RULE-INSTALL-<N> entries.
2. Create deploy/sysusers.d-ventd.conf (mirror sysusers.d-ventd-ipmi.conf).
3. Create deploy/ventd-recover.service (oneshot unit, runs cmd/ventd-recover
   binary which already exists).
4. Create deploy/install-contract_test.go with 4 subtests; RULE-INSTALL-04
   subtest uses t.Skip("filled in PR 2") until PR 2 lands.
5. Change web.listen default to 127.0.0.1:9999 (grep for the current default
   in internal/config/ — do not guess the filename).
6. Update deploy/README.md and CHANGELOG.md.

Commit at boundaries using conventional commits:
- feat(deploy): ship sysusers.d-ventd for User=ventd auto-creation
- feat(deploy): ship ventd-recover.service for OnFailure= reference
- fix(config): default web.listen to 127.0.0.1 to avoid TLS gate on fresh boot
- test(deploy): install-contract invariants bound to subtests
- docs(rules): add install-contract.md with RULE-INSTALL-01..04

After every edit: go test -race ./... && systemd-analyze verify deploy/*.service

Success condition:
  cd ~/ventd
  go test -race ./deploy/...
  systemd-analyze verify deploy/ventd.service deploy/ventd-ipmi.service deploy/ventd-recover.service
  systemd-sysusers --dry-run deploy/sysusers.d-ventd.conf 2>&1 | grep -c 'u ventd'
  go run ./tools/rulelint

Stop and surface if:
- ventd user creation requires more than a sysusers.d drop-in (probably it does not).
- ventd-recover binary at cmd/ventd-recover does not exist (it should — P3-RECOVER-01 landed).
- web.listen default lives in YAML defaults not Go code (adjust file touched, not approach).
- Any existing test fails due to the web.listen change (the change is deliberate; update the test).
- The install-contract parser grows past 100 lines (overthinking — mirror spec-01 PR 3's parser).
```

## Why this is cheap

- Pure Go, no hardware for PR 1. AppArmor HIL for PR 2 only.
- Ini parser pattern proven by spec-01 PR 3 — copy, don't invent.
- sysusers.d pattern proven by spec-01 PR 2a — one additional file, same shape.
- ventd-recover binary already exists (P3-RECOVER-01 landed); PR 1 only ships the unit that wraps it.
- Rule file follows exact format of hwmon-safety.md + ipmi-safety.md.
- AppArmor profile is bounded — scope is listed exhaustively in PR 2 above, no exploration.
