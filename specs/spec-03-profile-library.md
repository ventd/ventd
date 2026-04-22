# Spec 03 — Profile library schema + matcher

**Masterplan IDs this covers:** Extension of P1-FP-01 (already in `main`), P5-PROF-01, P5-PROF-02, and the foundation for the "profile flywheel" named in market-strategy wedge #1.
**Target release:** v0.5.0 — v0.7.0 rolling (v0.5 lands schema + matcher + 25 seed entries; v0.6 adds capture; v0.7 is the public flywheel launch).
**Estimated session cost:** Sonnet, ~6–10 sessions, $8–15 each. One Opus consult ($2–5) on schema design before freezing v1.
**Dependencies already green:** P1-FP-01 (fingerprint hwdb exists with 3 entries — this spec expands it substantially).

---

## Why this is the highest-leverage feature in the whole plan

Your stated vision: *"ventd figures out what pwm paths are exposed, calibrates the fans, and autogenerates the optimal fan curve for each fan based on thermal limits and power draw."* The profile library is **how ventd knows what "optimal" means without making the user tell it.** Every other feature compounds on this — calibration, learning, live-cal, even the IPMI vendor matrix.

This is the single thing that separates ventd from every competitor. fan2go doesn't have it. CoolerControl doesn't have it. Get the schema right now, before thousands of profiles are captured against a schema that needs migrating.

## Scope — what this session produces

Four PRs. PR 1 and 2 can run in parallel sessions. PR 3 depends on 1+2. PR 4 is the big one.

### PR 1 — Schema design freeze (P1-FP-01 extension)

**Purpose:** Lock the YAML schema for `internal/hwdb/profiles.yaml` before adding 25 entries to a schema that might need to change.

**Files:**
- `internal/hwdb/schema.go` (new — Go types for YAML parsing)
- `internal/hwdb/schema_test.go` (new — golden-file tests against fixture YAML)
- `internal/hwdb/profiles.yaml` (update to schema v1)
- `docs/hwdb-schema.md` (new — human-readable schema reference)
- `.claude/rules/hwdb-schema.md` (new — schema invariants bound to `TestSchema_Invariants`)

**Schema v1 fields per profile entry:**
```yaml
- id: "msi-meg-x570-unify"              # stable kebab-case, never changes
  schema_version: 1                      # migration anchor
  fingerprint:
    dmi_board_vendor: "Micro-Star International Co., Ltd."
    dmi_board_name: "MEG X570 UNIFY (MS-7C35)"
    dmi_board_version: ["1.0", "2.0"]    # list = any-match
    family: "x570-unify"                 # fuzzy-match anchor
    superio_chip: "nct6798d"             # kernel module after autoload
  hardware:
    fan_count: 6
    pwm_control: "nct6798d"
    temp_sensors: ["k10temp", "nct6798d", "nvme"]
    quirks:
      nct6798d_pwm3_broken: true         # this board's pwm3 reads fine but doesn't write
  defaults:
    cpu_sensor: "k10temp/Tctl"           # preferred for CPU fan mixing
    curves:
      - role: "cpu"
        points: [[40,30],[60,50],[75,80],[85,100]]
      - role: "case"
        points: [[30,20],[50,40],[70,70],[80,100]]
  sensor_trust:                          # from FEATURE-IDEAS FP-05
    - sensor: "nct6798d/temp4"
      trust: "untrusted"
      reason: "stuck-at reading on this board rev"
  contributed_by: "anonymous"            # or github handle if user opts in
  captured_at: "2026-04-22"
  verified: true                         # manual review flag
```

**Schema invariants (`.claude/rules/hwdb-schema.md`):**
1. `RULE-HWDB-01`: Every entry has `id`, `schema_version`, `fingerprint`, `hardware`. Missing fields reject at load time.
2. `RULE-HWDB-02`: `id` is unique across the file. Duplicate IDs reject at load.
3. `RULE-HWDB-03`: `schema_version` matches the parser's supported versions. Unknown version rejects with a migration hint.
4. `RULE-HWDB-04`: Curve points are monotonic non-decreasing in both temp and PWM axes. Non-monotonic reject at load.
5. `RULE-HWDB-05`: `pwm_control` value matches a known kernel module name (allowlist of ~20 modules). Unknown → reject (prevents typos).
6. `RULE-HWDB-06`: No PII fields in schema. No `smbios_uuid`, no `serial`, no `mac_address`, no fullname `contributed_by` unless handle is opt-in. Parser rejects any field not in the allowlist.
7. `RULE-HWDB-07`: Schema changes require a migration function in `internal/hwdb/migrate.go`. A test fails if a new `schema_version` is added without a migration.

### PR 2 — Matcher algorithm (P1-FP-01 finalisation)

**Files:**
- `internal/hwdb/match.go` (extend)
- `internal/hwdb/match_test.go` (already exists per T-FP-01 — extend coverage)
- `internal/testfixture/fakedmi/**` (already exists — add 10 more board fixtures)

**Algorithm — deterministic three-tier match:**
1. **Exact match:** DMI board vendor + name + version triple is in the DB exactly. Return that entry.
2. **Family match:** board vendor + board name (any version) is in the DB. Return that entry with a `match_confidence = "family"` flag surfaced to the UI.
3. **Heuristic match:** Super I/O chip ID + PCI subsystem ID pair matches a board family (even if the specific board isn't in DB). Return a "generic profile" for that chip family with `match_confidence = "heuristic"`.
4. **No match:** Return nil, controller falls back to calibration-generated curves with conservative defaults.

**Test coverage (extending T-FP-01):**
- Exact match test: 5 boards with known DMI strings → correct profile returned.
- Family match test: board vendor + name present, version "3.0" absent from DB version list → correct profile with family flag.
- Heuristic match test: DMI strings are gibberish, but chip is `nct6798d` → generic nct6798d profile returned.
- No-match test: zero overlap with DB → nil + fallback path exercised.
- Regression tests: every DMI string seen in a past issue report becomes a test row. (Use `ventdmasterplan.md` §R19 pattern.)

### PR 3 — Seed entries (25 boards minimum)

**File:**
- `internal/hwdb/profiles.yaml` (populate)

**Target distribution (25 entries minimum, aim for 30):**

| Vendor | Boards | Rationale |
|---|---|---|
| MSI | 4 (X570, X670E, Z790, B650) | Phoenix-desktop is MSI; highest-quality data |
| ASUS | 4 (Prime X670E, ROG Strix X870E, TUF B650, Prime B760) | Largest DIY market share |
| Gigabyte | 3 (X870E Aorus, B650 Aorus Elite, Z790 Aorus) | Distinct Super I/O variants |
| ASRock | 2 (X670E Taichi, B650M PG Lightning) | ASRock quirks are real |
| Supermicro | 3 (X11, X12, X13 — server) | IPMI cross-ref |
| Dell | 2 (PowerEdge R740, R750) | IPMI cross-ref |
| Framework | 2 (13 AMD, 16 AMD) | Laptop EC cross-ref |
| Raspberry Pi | 1 (Pi 5) | SBC cross-ref |
| HPE | 1 (ProLiant DL380 Gen10) | For the error-path test |
| Generic heuristic profiles | 3 (nct6798d-generic, it8628e-generic, nct6687-generic) | Fallback tier |

**Contribution process for each entry:**
1. DMI strings from real hardware (yours, Scout's, validation lab).
2. Curves pulled from `/etc/fancontrol` configs found on the public web for that specific board, cross-referenced for sanity.
3. Every entry starts with `verified: false`. Flip to `true` only after a human runs ventd against the board and confirms the curves don't cause thermal issues.

**Do NOT:**
- Invent curves. If a curve isn't from a verified source, mark the entry `verified: false` and ship with `defaults.curves: []` (empty). Matcher returns fingerprint only; controller calibrates.
- Include any PII. Pre-commit hook (T-META-02 or equivalent) should grep for `smbios`, `serial`, `uuid`, common name patterns.

### PR 4 — Capture pipeline (P5-PROF-01, P5-PROF-02)

**Files:**
- `internal/hwdb/capture.go` (new)
- `internal/hwdb/capture_test.go` (new)
- `internal/hwdb/anonymise.go` (new — PII stripping)
- `internal/hwdb/anonymise_test.go` (new — fuzz target with 100-sample corpus)

**Behaviour:**
1. After successful calibration, ventd writes a candidate profile to `/var/lib/ventd/profiles-pending/<fingerprint-hash>.yaml`.
2. File permissions: `0640`, owner `ventd:ventd`. No world-read.
3. Anonymisation runs BEFORE write, not after — no plaintext-PII window.
4. Anonymisation fields stripped: `smbios_uuid`, `chassis_serial`, `system_serial`, `baseboard_serial`, `mac_address`, `hostname`, `username` from any home-dir paths, any string >3 bits of unexpected entropy (Shannon entropy check against known-structural fields).
5. No network. No PR. No background HTTP call. Ever. The user takes the file and does what they want with it. An explicit `ventd --submit-profile <id>` CLI flag is P5-PROF-03 (future spec) — not in scope here.
6. Fuzz target: `FuzzAnonymise` — 100 sample inputs with randomly embedded PII patterns. DoD: zero leakage across all 100 samples.

**Safety invariants (extend `.claude/rules/hwdb-schema.md`):**
- `RULE-HWDB-08`: Capture writes go to `/var/lib/ventd/profiles-pending/` only. Never to the live `profiles.yaml` at runtime.
- `RULE-HWDB-09`: Capture cannot run if PII anonymiser fails to initialise. Fail closed.
- `RULE-HWDB-10`: No capture file ever contains a field not in the schema v1 allowlist.

## The Opus consult — before PR 1

Before finalising the schema, have Opus review these specifically:

1. **Migration path.** If you're wrong about schema v1, what does v2 cost? Migrations should be mechanical.
2. **Curve representation.** Piecewise linear `[[temp,pwm],...]` vs Bezier vs formula. Which survives adding PI controller gains and MPC hints in v2? Consider: can you store a PI setpoint + gains in the same schema as a piecewise curve without a type discriminator mess?
3. **Fingerprint stability.** DMI strings are surprisingly unstable — BIOS updates change `board_version`. Is your three-tier match robust to the real pattern of DMI drift, or are you going to regret tier 1 being exact-match?
4. **Community governance.** The schema is a contract with every future contributor. Is there anything in v1 that looks cute now and becomes a maintenance burden at 500 entries?

Budget one hour. Walk out with: final schema frozen, a written migration policy, and an explicit "things we said no to for v1" list.

## Definition of done

### PR 1
- [ ] `go test -race ./internal/hwdb/...` passes.
- [ ] Schema v1 documented in `docs/hwdb-schema.md` with an example entry.
- [ ] `.claude/rules/hwdb-schema.md` has 7 invariants, all bound.
- [ ] Load test: a malformed YAML fails with a clear error message naming the bad field and the line number.

### PR 2
- [ ] Matcher returns correct tier for all 4 tier scenarios against fixture DMI.
- [ ] No nondeterminism — same input always returns same output (no map-ordering bugs).
- [ ] Benchmark: matcher runs in <1ms for a 500-entry DB. (Tests budget-fail above 5ms to catch regressions.)

### PR 3
- [ ] ≥25 entries in `profiles.yaml`, all passing `TestSchema_Invariants`.
- [ ] Every entry has a source citation in a comment above it (fancontrol config URL, issue number, or "phoenix-desktop 2026-04-XX").
- [ ] Pre-commit PII grep returns zero matches.

### PR 4
- [ ] `FuzzAnonymise` runs for 10 minutes, zero PII leakage.
- [ ] Capture file written with correct permissions under `ventd` user (systemd-run integration test).
- [ ] `ventd --dry-run-capture` flag produces the YAML to stdout without writing — for user inspection.

## Explicit non-goals

- **No network submission flow.** P5-PROF-03 territory.
- **No UI for reviewing captured profiles.** Phase 8.
- **No GitHub-repo-based `ventd/hardware-profiles` refresh.** P1-FP-02 already covers the shape of this; this spec doesn't expand on it.
- **No "auto-generate curve from hardware specs" ML feature.** That's Phase 5+4 combined with calibration — not hwdb's job.

## CC session prompt — copy/paste this

```
Read /home/claude/specs/spec-03-profile-library.md. This is a four-PR spec;
PR 1 and PR 2 can be done in parallel sessions but PR 3 depends on both and
PR 4 depends on PR 1.

Before starting PR 1: confirm I have separately frozen the schema via an
Opus review. The frozen schema is in /home/claude/specs/spec-03-schema-v1.md
(I will add this file). Read that file first — it is normative.

For PR 3, every profile entry MUST cite its source. Do not invent curves. If
you don't have a verified curve source for a board, ship the entry with
`defaults.curves: []` and `verified: false` — that's correct behaviour, not
a bug.

For PR 4, the anonymiser is safety-critical. Every field stripped must be in
a test case. Write the tests first, then the code. Do not let a "clever"
regex replace the explicit allowlist.

Use Sonnet throughout. Commit at every green-test boundary.
```

## Cost discipline notes

- PR 3 is the token-expensive one because it's 25 YAML blobs. Do it in 3 sessions of ~10 entries each, not one session of 25. Context stays small.
- The anonymiser fuzz corpus (PR 4) — have Haiku generate the 100 sample inputs. It's mechanical, pattern-matched work. Sonnet writes the code; Haiku builds the corpus.
- Do NOT let CC propose schema changes during PR 3 or PR 4. The schema is frozen after PR 1. Schema changes require a new migration PR.
