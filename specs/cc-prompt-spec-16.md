# CC prompt — spec-16 persistent state foundation (v0.5.0.1)

**Target model:** Sonnet
**Estimated cost:** $15-25
**Branch:** `spec-16-persistent-state`
**PR target:** `main`
**Tag after merge:** `v0.5.0.1`

---

## Mission

Implement spec-16 persistent state foundation per
`specs/spec-16-persistent-state.md`. Single PR. Single tight scope.
Produce the three storage primitives (KV, Blob, Log), version
sentinel, sysusers/AppArmor integration, RULE-STATE-* bindings, and
synthetic CI tests.

This is foundation infrastructure for the smart-mode patch sequence
(v0.5.1 through v0.5.10 → v0.6.0). Subsequent patches add consumers;
this patch ships empty consumers — primitive only.

---

## Files to create

### Production code

- `internal/state/state.go` — top-level State struct, opens
  KV+Blob+Log primitives, exposes the State interface to daemon.
- `internal/state/kv.go` — YAML KV store implementation with
  tempfile+rename+fsync atomic write, in-process RWMutex, transaction
  support.
- `internal/state/blob.go` — binary blob store with 16-byte header
  (magic "VBLB", schema_version, length), SHA256 payload verification,
  atomic write.
- `internal/state/log.go` — append-only log store with length-prefix
  records, CRC32 IEEE checksums, `O_APPEND | O_DSYNC`, rotation by
  size+age+keep-count, optional gzip compression of rotated files.
- `internal/state/version.go` — schema version sentinel + migration
  registry (per (from, to) pair).
- `internal/state/pidfile.go` — multi-process detection via
  `/var/lib/ventd/ventd.pid`.

### Test code

- `internal/state/state_test.go` — RULE-STATE-01 through RULE-STATE-10
  subtests, 1:1 with rules. Each subtest named
  `TestRULE_STATE_NN_<descriptor>`.

### Rules

- `.claude/rules/RULE-STATE-01.md` through `.claude/rules/RULE-STATE-10.md`
  — each carries `Bound:` line pointing at the corresponding subtest.
  No `<!-- rulelint:allow-orphan -->` markers; subtests ship in same PR.

### Install contract updates

- `deploy/sysusers.d/ventd.conf` — already exists per spec-06; verify
  `ventd` user/group. No changes expected.
- `deploy/tmpfiles.d/ventd.conf` — add three lines:
  ```
  d /var/lib/ventd        0755 ventd ventd -
  d /var/lib/ventd/models 0755 ventd ventd -
  d /var/lib/ventd/logs   0755 ventd ventd -
  ```
- `deploy/apparmor/ventd` — extend write-allowlist to include
  `/var/lib/ventd/models/**` and `/var/lib/ventd/logs/**` and
  `/var/lib/ventd/state.yaml*` and `/var/lib/ventd/ventd.pid`.

### Daemon integration (minimal)

- `cmd/ventd/main.go` — add `state.State` initialisation after
  config load, before HAL init. Verify state directory + permissions.
  No consumers wired yet.

### Documentation

- `CHANGELOG.md` — entry under `## [Unreleased]`:
  ```
  ### Added
  - Persistent state foundation: KV, binary blob, append-only log
    primitives at `/var/lib/ventd/`. Foundation for smart-mode patch
    sequence. (#PR-NUMBER)
  ```
- `docs/architecture/persistent-state.md` — short developer-facing
  doc describing the three stores, when to use which, and the
  `state.State` interface. ~150 lines, no marketing.

---

## Files NOT to touch

- `internal/calibration/cache/` — existing calibration cache stays as-is.
  Migration to KV store is post-v0.5.0.1 work.
- `internal/diag/` — diag bundle reads existing log paths; do not
  reroute through new log store yet.
- `web/` UI surfaces — no UI for state inspection in this patch.
- Existing `internal/config/` — config loading is independent of state
  persistence.
- spec-15 framework — F1 toggle-detection backfill is post-v0.5.0.1.

---

## Success conditions

PR is mergeable when **all** of these hold:

1. `go build ./...` clean.
2. `go test ./...` clean. New `internal/state/` tests pass.
3. `golangci-lint run ./...` clean. No new exclusions.
4. `tools/rulelint` clean. RULE-STATE-01 through RULE-STATE-10 all
   bind to subtests in `internal/state/state_test.go`.
5. CI all 17 status checks green.
6. CHANGELOG updated under `## [Unreleased]`.
7. `go list -deps ./cmd/ventd | grep state` shows `internal/state`
   linked into the daemon binary.
8. Daemon starts cleanly on Proxmox HIL with empty `/var/lib/ventd/`
   (verifies bootstrap path).
9. Daemon starts cleanly on Proxmox HIL with pre-populated
   `/var/lib/ventd/state.yaml` from a previous run.

---

## Invariants that must hold

- **CGO_ENABLED=0.** No new C dependencies. YAML via existing
  `gopkg.in/yaml.v3`. SHA256 from `crypto/sha256`. CRC32 from
  `hash/crc32`. Gzip from `compress/gzip`. All stdlib or already-
  vendored.
- **Linear history on main.** Squash-merge.
- **Conventional commits.** Subject lines like:
  - `feat(state): KV store with atomic write`
  - `feat(state): binary blob store with SHA256 verification`
  - `feat(state): append-only log with rotation`
  - `feat(state): version sentinel and migration registry`
  - `feat(state): pid file for multi-process detection`
  - `test(state): RULE-STATE-01..10 subtests`
  - `chore(deploy): tmpfiles entries for /var/lib/ventd subdirs`
  - `chore(apparmor): extend profile for state subdirs`
  - `docs(state): developer architecture note`
- **No subagents.** Single-session implementation.
- **No Opus.** This is mechanical implementation against a tight spec.
  Sonnet only.
- **`.claude/rules/RULE-STATE-*.md` files MUST have correct Bound:
  lines pointing at real subtests in this PR.** No allow-orphan
  markers needed; tests ship in same PR.
- **Multi-process detection MUST exit second process cleanly with
  diagnostic** — RULE-STATE-06 explicit subtest.
- **Permission repair on read** — if state.yaml exists with mode
  0600, daemon repairs to 0640 rather than refusing. RULE-STATE-09
  subtest.

---

## Verification steps for Phoenix (post-merge)

1. **Local build sanity:**
   ```
   git pull origin main
   go build ./cmd/ventd
   go test ./internal/state/...
   ```

2. **Proxmox HIL bootstrap test (empty state):**
   ```
   ssh proxmox-host
   sudo systemctl stop ventd
   sudo rm -rf /var/lib/ventd
   sudo systemctl start ventd
   sudo systemctl status ventd          # must be active
   ls -la /var/lib/ventd/               # must contain models/, logs/
   stat -c '%a %U:%G' /var/lib/ventd    # 755 ventd:ventd
   ```

3. **Proxmox HIL persistence test (pre-populated state):**
   ```
   sudo systemctl stop ventd
   echo "schema_version: 1
   ventd:
     install_id: test-uuid" | sudo tee /var/lib/ventd/state.yaml
   sudo chown ventd:ventd /var/lib/ventd/state.yaml
   sudo chmod 0640 /var/lib/ventd/state.yaml
   sudo systemctl start ventd
   sudo systemctl status ventd          # must be active
   sudo cat /var/lib/ventd/state.yaml   # must still contain test-uuid
   ```

4. **Multi-process detection test:**
   ```
   sudo -u ventd /usr/bin/ventd &       # second instance
   # verify exits within 2s with diagnostic mentioning PID file
   ```

5. **Permission repair test:**
   ```
   sudo systemctl stop ventd
   sudo chmod 0600 /var/lib/ventd/state.yaml
   sudo systemctl start ventd
   stat -c '%a' /var/lib/ventd/state.yaml   # must be 640 after start
   ```

If all five verifications pass, PR is HIL-clean. Tag v0.5.0.1.

---

## Tag procedure (post-HIL)

Per `release-process.md` and tag discipline rules in memory:

```
git tag --sort=-v:refname | head -5     # confirm v0.5.0 is latest tag
gh pr view <PR-NUMBER> --json mergeCommit
git pull origin main                     # confirm squash commit on main
git tag -a v0.5.0.1 -m "Persistent state foundation (spec-16)"
git push origin v0.5.0.1
gh release list                          # verify Release workflow ran
```

CHANGELOG migration: move v0.5.0.1 entry from `## [Unreleased]` to
`## [v0.5.0.1] - YYYY-MM-DD` in same commit as tag if pre-drafted, or
post-tag PR.

---

## Out of scope reminders

This PR does NOT:

- Migrate the existing calibration cache.
- Wire up any consumer (passive observation, signature library, etc).
- Add UI for state inspection.
- Implement backup/restore.
- Add encryption.
- Touch the diag bundle redactor.
- Add database backends.

Each of those is a separate post-v0.5.0.1 patch.

---

## Why this estimate ($15-25)

Reasoning per memory cost calibration:

- Spec is comprehensive and unambiguous — model transcribes rather
  than explores.
- Three storage shapes are independent of each other; can be built
  serially without back-and-forth.
- RULE-STATE-* bindings are mechanical (1:1 subtest mapping).
- Sysusers/AppArmor changes are minor extensions of existing files.
- No HAL touches, no controller touches, no UI touches.

Pad +50% if AppArmor profile causes friction (it sometimes does on
distros with nuanced default profiles). Hard cap: if CC is past $30
and not converged, stop the session, surface what's stuck, redesign
in chat.
