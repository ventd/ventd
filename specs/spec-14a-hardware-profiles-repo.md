# spec-14a — Upstream `hardware-profiles` repo design

**Status**: Draft, 2026-04-26
**Targets**: bootstrap as v0.5.x patch (no ventd code change), consumption lands v0.6.x alongside spec-04
**Pairs with**: spec-14b (web UI submission flow)
**Related**: #650 (P5-PROF-03), spec-03 (hwdb), spec-04 (PI controller + auto-load)

---

## Problem

Two tightly-related issues that have to be solved together:

1. **Existing repo is a stub.** `ventd/hardware-profiles` has 5 commits, an 806-byte `profiles.yaml`, no schema, and a README that promises "blessed contribution flow once ventdctl exists". Cannot be consumed by ventd in current state.
2. **No submission target.** Capture pipeline (PR #649) writes `profiles-pending/<fp>.yaml` to disk but has nowhere upstream to send them. Issue #650 asks for a design.

This spec defines **what the upstream repo looks like**. spec-14b defines **how ventd contributes to it**.

---

## Why per-board files (not single profiles.yaml)

The original repo description ("One YAML file") was wrong for three reasons that surface as soon as the repo has more than ~10 entries:

| | Single profiles.yaml | Per-board files |
|---|---|---|
| GitHub URL prefill cap (~8KB) | breaks ~10 entries in | always works, individual file <5KB |
| PR diff readability | review a 50KB merge | review one new file |
| Merge conflicts | constant on hot vendors | structurally impossible |
| Maintainer reformatting burden | high (must keep YAML sorted/grouped) | none |
| ventd consumption | read whole file | read index, fetch on demand |

The **discovery** downside (no single grep target) is solved by a CI-generated `index.yaml` that contributors never edit.

---

## Repo layout

```
ventd/hardware-profiles/
├── README.md                          # contributor-facing
├── LICENSE                            # GPL-3.0 (already present)
├── CONTRIBUTING.md                    # how submissions work (points at ventd web UI)
├── schema/
│   ├── profile-v1.json                # JSON Schema draft 2020-12
│   └── README.md                      # schema versioning rules
├── profiles/
│   ├── msi/
│   │   └── meg-x670e-ace.yaml
│   ├── asus/
│   │   └── rog-strix-x670e-e.yaml
│   ├── lenovo/
│   │   ├── legion-pro-5-16irx9.yaml
│   │   └── thinkpad-t14-gen-4.yaml
│   ├── raspberry-pi/
│   │   └── pi5-bcm2712.yaml
│   └── ...
├── index.yaml                         # auto-generated, do NOT edit
├── .github/
│   ├── workflows/
│   │   ├── validate-pr.yml            # JSON Schema check + duplicate-fingerprint check
│   │   └── reindex-on-merge.yml       # regenerates index.yaml on merge to main
│   └── PULL_REQUEST_TEMPLATE.md
├── tools/
│   └── reindex.py                     # called by reindex-on-merge.yml
└── .gitattributes                     # *.yaml text eol=lf (already present)
```

### Naming convention

Profile filename: `<board-slug>.yaml`, lowercased, hyphenated. Slug derived from the board's DMI `product_name` field, with these rules:

- `MEG X670E ACE` → `meg-x670e-ace`
- `ROG STRIX X670E-E GAMING WIFI` → `rog-strix-x670e-e-gaming-wifi`
- Spaces → hyphens; non-alphanumeric (except hyphens) → stripped

Slug collisions across vendors are impossible because the directory disambiguates. Slug collisions within a vendor (e.g., two boards with same product_name but different revisions) get a suffix: `b650m-aorus-elite-ax.yaml`, `b650m-aorus-elite-ax-rev2.yaml`.

ventd computes the expected slug at submission time and offers it as the filename — but a maintainer can rename during PR review.

---

## Profile schema v1

YAML, validated against `schema/profile-v1.json`.

```yaml
schema_version: 1
fingerprint:
  hash: "a3f1b2c4d5e6f7a8"          # 16-char hex, SHA-256 prefix of v1 input tuple
  dmi:
    sys_vendor: "Micro-Star International Co., Ltd."
    product_name: "MS-7E13"
    product_version: "1.0"
    board_vendor: "Micro-Star International Co., Ltd."
    board_name: "MEG X670E ACE (MS-7E13)"
    board_version: "1.0"
  cpu:
    model_name: "AMD Ryzen 9 7950X3D 16-Core Processor"
    core_count: 16
  bios_version_observed: "1.A0"     # not part of fingerprint; informational

board:
  vendor: "MSI"                     # Display name, normalised
  product: "MEG X670E ACE"
  family: "AM5"                     # Optional, freeform
  socket: "AM5"                     # Optional, freeform

chips:
  super_io:
    - name: "NCT6687D"              # As reported by hwmon
      hwmon_driver: "nct6687"

channels:
  - hwmon_path: "hwmon3/pwm1"        # Path under /sys/class/hwmon/, relative
    role: "cpu_fan"                  # enum: cpu_fan, case_fan, pump, gpu_fan, aio_fan, vrm_fan, exhaust
    label: "CPU_FAN1"                # Vendor label if known; redacted if user-typed
    fan_rpm_path: "hwmon3/fan1"      # Optional but recommended
    calibration:
      start_pwm: 35
      stop_pwm: 25
      max_rpm: 2400
      probe_method: "ramp_then_settle" # Reserved; see spec-03 PR 2b
      probed_at: "2026-04-26T12:34:56Z"
    default_curve:
      sensor: "k10temp/Tctl"        # hwmon-relative
      points:
        - { temp_c: 30, pwm: 30 }
        - { temp_c: 50, pwm: 50 }
        - { temp_c: 70, pwm: 80 }
        - { temp_c: 85, pwm: 100 }
      hysteresis_c: 3

  - hwmon_path: "hwmon3/pwm2"
    role: "case_fan"
    # ... etc

predictive_hints:                    # Reserved for spec-05 consumers; freeform v1
  thermal_mass_class: null           # Will be populated by spec-08 ARX learner
  pump_priority: null

metadata:
  contributor: "PhoenixDnB"          # GitHub handle; set by user in submission UI
  contributor_notes: ""              # Freeform, optional
  ventd_version: "0.5.0"             # Captured at submission time
  kernel: "6.8.0-49-generic"         # uname -r, redacted of hostname
  captured_at: "2026-04-26T12:34:56Z"
  submitted_at: "2026-04-26T13:00:00Z"
```

### Field rules

- `schema_version`: integer, currently `1`. Breaking changes bump to 2 with a parallel `profile-v2.json`. ventd reads both during the deprecation window.
- `fingerprint.hash`: must match SHA-256 prefix of the v1 input tuple as defined in `internal/hwdb/fingerprint.go`. Submission UI computes this; CI validates.
- `fingerprint.dmi.*`: free-text DMI strings. Required as published by the kernel — *no PII redaction here*, these are vendor-published board strings. Validation rejects strings containing `/home/`, `@`, hostname-like patterns.
- `fingerprint.cpu.*`: model_name kept verbatim from `/proc/cpuinfo`. core_count: integer.
- `bios_version_observed`: not in fingerprint hash (BIOS updates shouldn't invalidate profiles), but recorded for forensics.
- `channels[]`: at least one. Each must have `hwmon_path`, `role`, `calibration.start_pwm`, `calibration.stop_pwm`, `calibration.max_rpm`. Other fields optional.
- `channels[].label`: if user-typed (RULE-FINGERPRINT-09 territory), redactor strips it. Vendor labels (regex match against known patterns) kept.
- `channels[].default_curve.points`: monotonic non-decreasing PWM by temp_c, validated by JSON Schema `uniqueItems` + custom CI check.
- `metadata.contributor`: GitHub handle; `^[A-Za-z0-9-]{1,39}$`. Set by submission UI; CI doesn't verify against authorship of the PR (intentional — anonymous contributions allowed via "anonymous" placeholder).
- `metadata.kernel`: `uname -r` output, with hostname stripped if present.

### Privacy invariants enforced by JSON Schema + CI

The schema rejects (PR validation fails) any profile containing:

- `/home/<anything>` paths
- IPv4/IPv6 address patterns
- MAC address patterns (`xx:xx:xx:xx:xx:xx`)
- `@` characters in any string field except `metadata.contributor` if it looks like an email
- USB physical paths matching `usb-X-Y.Z` where the path is in a label field
- Hostname-shaped strings in fields not on the allowlist

These are belt-and-suspenders: ventd's redactor strips them at submission time (P1-P9 framework from PR #639), CI rejects any that slip through.

---

## index.yaml

Generated by `tools/reindex.py` on merge to main. Contributors **never** commit changes to it; CI does.

```yaml
schema_version: 1
generated_at: "2026-04-26T13:00:00Z"
profile_count: 17
profiles:
  - fingerprint_hash: "a3f1b2c4d5e6f7a8"
    path: "profiles/msi/meg-x670e-ace.yaml"
    board_vendor: "MSI"
    board_product: "MEG X670E ACE"
    contributor: "PhoenixDnB"
    submitted_at: "2026-04-26T13:00:00Z"
  - fingerprint_hash: "b4e2c3d5e6f7a8b9"
    path: "profiles/asus/rog-strix-x670e-e.yaml"
    board_vendor: "ASUS"
    board_product: "ROG STRIX X670E-E GAMING WIFI"
    contributor: "anonymous"
    submitted_at: "2026-04-26T13:15:00Z"
  # ...
```

ventd's consumption flow:

1. On startup with `hwdb.allow_remote: true`: fetch `https://raw.githubusercontent.com/ventd/hardware-profiles/main/index.yaml` (CDN-cached, ~10KB even at 1000 profiles).
2. Compute local fingerprint hash via existing `internal/hwdb/fingerprint.go`.
3. Look up hash in index. If matched, fetch `https://raw.githubusercontent.com/ventd/hardware-profiles/main/<path>`.
4. Cache at `/var/lib/ventd/hwdb-cache/<hash>.yaml`. Stat-cache for ETag-style refresh.
5. With `hwdb.allow_remote: false` (default for now), skip steps 1-3, fall back to embedded.

The embedded fallback at build time uses `go:embed` against a snapshot of `index.yaml` + the matched profiles. Snapshot updated per ventd release. This is the "lags but works offline" path.

---

## Validation (`.github/workflows/validate-pr.yml`)

PR-time checks:

1. **Schema**: every modified `profiles/*/*.yaml` validates against `schema/profile-v1.json` using `ajv` or `python-jsonschema`.
2. **Privacy regex sweep**: greps modified files for patterns in the privacy invariants. Any hit fails CI with the file + line number + matched pattern.
3. **Fingerprint uniqueness**: checks that the new file's `fingerprint.hash` does not already exist in any other file under `profiles/`. Duplicates fail CI; resolution is "the existing one wins, this PR's contributor edits the existing file instead."
4. **Filename matches slug rule**: `<dir>/<slug>.yaml` where slug derived from `board.product` per the naming convention.
5. **YAML format**: `yamllint` with strict config (LF endings already enforced via `.gitattributes`).

Optional (later): a maintainer-only check that re-runs `ventd diag --validate-profile <path>` against the pending profile, exercising real schema parsing inside ventd. Defers to spec-14b's diag UI.

---

## Reindex (`.github/workflows/reindex-on-merge.yml`)

On push to main:

1. Run `tools/reindex.py`, which walks `profiles/`, parses each YAML, writes a fresh `index.yaml`.
2. If `index.yaml` changed, commit it back to main as `[bot] reindex profiles` with the bot identity.
3. No tag, no release. The repo is the source of truth; ventd polls it.

`tools/reindex.py` is ~50 lines of stdlib Python. No dependencies. Reproducible — runs in dev too via `python3 tools/reindex.py`.

---

## Governance

**Maintainer-curated until ventd v1.0.** Phoenix reviews every PR. Acceptance criteria:

- CI green (schema + privacy + uniqueness pass)
- Profile not obviously wrong (e.g., start_pwm < stop_pwm, max_rpm < 100)
- Calibration timestamp within last 90 days (encourages fresh data; arbitrary, can revisit)
- No vendor name normalisation issues

**Post-v1.0**: revisit auto-merge with 7-day quarantine. Out of scope for this spec.

**Conflict resolution**: when two contributors submit the same fingerprint, the existing entry wins. Second contributor is asked to PR an edit if their data is meaningfully different (e.g., better calibration data on the same board, second BIOS version observed).

---

## Versioning

The repo is **not tagged**. There is no `v1.0` of the catalog. The schema is versioned (`schema_version: 1` in each file); the catalog itself is a rolling head.

ventd consumption is decoupled: a ventd binary built at version `X.Y.Z` knows about `schema_version: 1`. When schema v2 ships, ventd reads both for one minor release cycle, then deprecates v1 read support. Profiles in the repo can stay at v1 indefinitely if no upgrade is needed; old profiles never get auto-upgraded.

---

## Bootstrap PR plan (one CC session, ~$5-10)

Single PR against `ventd/hardware-profiles` adding everything except actual profiles:

```
+ schema/profile-v1.json
+ schema/README.md
+ tools/reindex.py
+ .github/workflows/validate-pr.yml
+ .github/workflows/reindex-on-merge.yml
+ .github/PULL_REQUEST_TEMPLATE.md
+ CONTRIBUTING.md
+ profiles/.gitkeep
+ index.yaml                         # initial empty: profile_count: 0
- profiles.yaml                       # remove the stub
~ README.md                           # rewrite to reflect new layout
```

This is mechanical. Sonnet CC, 30-60 minutes. Acceptance: PR validation workflow runs successfully against itself; reindex workflow generates the empty index correctly on merge.

After this PR: the repo is ready to receive profile contributions, but ventd doesn't know how to send them yet — that's spec-14b.

---

## Out of scope

- Profile signing / GPG provenance — separate spec, post-v1.0
- Multi-fingerprint profiles (one YAML for boards that share calibration) — possible v2 schema feature
- Profile inheritance / overlays — complexity not justified yet
- Mirror/CDN strategy beyond raw.githubusercontent.com — premature
- Maintainer review tooling (ventd-side dashboard for incoming PRs) — separate spec, mine to think about later

---

## Subtests / RULE bindings

This spec is for the **upstream repo**, not ventd code, so it doesn't generate `.claude/rules/*.md` bindings. Validation is enforced by JSON Schema + CI in the upstream repo, which is its own test layer.

The ventd-side rules (RULE-PROF-CONSUME-NN for fetching, parsing, caching) live in spec-14b.
