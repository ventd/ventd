# spec-v0_6_0-catalog-harvest — operator-friendly hardware names + agent-driven catalog growth

## Problem statement

Operators see `pwm3` and `Fan 1` instead of `CPU_FAN1` and `CHA_FAN_REAR`. The
hardware-database catalog (`internal/hwdb/profiles-v1.yaml`) supports this
metadata but only ~30 boards have full friendly-name coverage today. Manually
contributing per-board entries is the same trap fancontrol's pwmconfig fell
into: contributions are slow, sparse, and biased toward the boards
contributors happen to own.

Phoenix locked **option C aggressive friendly-naming** in #791: catalog growth
is backed by sub-agent data pulls, not user submissions. This is ventd's
competitive moat against fancontrol (manual contributions, slow) and
FanControl-Windows (no Linux catalog at all).

## Goal of this spec

Define the **harvest contract**, the **on-disk schema** the harvest output
lands in, and the **validation rules** that gate auto-merged catalog updates.
Implementation lands across follow-up PRs; this spec sets the boundaries so
those PRs can land independently.

Out of scope for this spec:
- Specific source-page selectors or extraction prompts (those live in
  `tools/catalog-harvest/<source>/`).
- The CI cron infrastructure (a separate ops task once the manual harvest
  proves out).
- Image / photo handling (post-v0.6.0 — see "Future work").

## Architecture

```
┌────────────────────────────┐         ┌────────────────────────────────┐
│ tools/catalog-harvest/     │  fetch  │ Source pages                   │
│   <source>/agent.md        ├────────►│  • ASUS / MSI / Gigabyte       │
│   <source>/extract.go      │         │  • lm-sensors archive          │
│                            │◄────────┤  • dmidecode SDK databases     │
│ Per-source extractor.      │ struct  │  • OEM service manuals (PDF)   │
└──────────┬─────────────────┘         └────────────────────────────────┘
           │ writes harvested entries
           ▼
┌────────────────────────────┐
│ internal/hwdb/             │
│   profiles-pending/        │  ← per-board YAML, schema_version: 1, awaiting validation
│     <fingerprint>.yaml     │
└──────────┬─────────────────┘
           │ tools/catalog-merge/  validates + diffs + opens PR
           ▼
┌────────────────────────────┐
│ internal/hwdb/             │
│   profiles-v1.yaml         │  ← live catalog; hand-curated entries protected
└────────────────────────────┘
```

The pending directory `internal/hwdb/profiles-pending/` is the buffer between
harvest output and live catalog. Harvested entries land there first; the merge
tool validates each, diffs against the live catalog, and opens a PR per harvest
batch. **No automatic merge to `profiles-v1.yaml`** — the PR review step is
required for the first 6 months of harvest operation; automation is gated on
field experience.

## Harvest output schema

Every harvested entry MUST be a single YAML file under
`internal/hwdb/profiles-pending/` keyed by the board fingerprint hash:

```yaml
schema_version: 1
fingerprint:
  dmi_board_vendor: "ASUSTeK COMPUTER INC."
  dmi_board_name:   "PRIME Z690-A"
  dmi_product_name: "" # blank when board != product (typical desktop)
hardware:
  pwm_control: "nct6798"
  fans:
    - id: "CPU_FAN1"
      friendly_name: "CPU cooler header"
      header_color: "white"
      sysfs_pwm: "pwm1"
      sysfs_input: "fan1_input"
      stall_pwm_min: 30
      max_rpm_typical: 2200
      role: "cpu"
    - id: "CHA_FAN1"
      friendly_name: "Front intake (chassis 1)"
      header_color: "black"
      sysfs_pwm: "pwm2"
      sysfs_input: "fan2_input"
      role: "case_intake"
    # ... etc per fan header
contributed_by: "harvest:asus.com:2026-05-04"
captured_at: "2026-05-04T11:30:00Z"
verified: false
provenance:
  source_url:    "https://www.asus.com/motherboards/prime-z690-a/spec/"
  source_class:  "manufacturer_product_page"
  agent_version: "v1"
  confidence:    0.85       # 0.0 — 1.0; 1.0 = identity-checked against board photo
  evidence:
    - "manual page 12: 'Refer to Connectors and Buttons (1-8) for header positions'"
    - "spec table row 'Fan/Pump Connectors' lists 4× CHA_FAN, 1× CPU_FAN, 1× CPU_OPT, 1× AIO_PUMP"
```

The `provenance` block is the load-bearing addition over today's schema. Every
harvested entry carries:
- `source_url`: where the data came from (auditable).
- `source_class`: one of a closed set (see RULE-CATALOG-HARVEST-02 below).
- `agent_version`: tags entries to the harvest agent build that produced them
  (lets us roll forward without re-validating old entries).
- `confidence`: 0.0–1.0 score the agent assigns based on signal strength.
- `evidence`: short text excerpts the agent extracted to justify the entry.
  Operator-readable so a reviewer can verify without re-fetching the source.

The matcher (RULE-FINGERPRINT-* family) MUST treat `confidence < 0.7` entries
as advisory: friendly-name promotion happens, but in the doctor surface the
operator sees a "harvested data, low confidence — please verify" badge.

## Source classes (closed set)

```
RULE-CATALOG-HARVEST-02: source_class is one of:
  manufacturer_product_page  — official spec page on ASUS/MSI/etc.
  manufacturer_manual_pdf    — service manual PDF, page-numbered evidence
  lm_sensors_archive         — kernel.org/doc/sensors/ entries
  dmidecode_db               — dmidecode SDK known-good board lists
  oem_kbase_article          — Dell / HP / Lenovo support KB
  community_wiki             — Arch / NixOS hwdb wiki entries
  bios_dump                  — extracted from a BIOS image (DMI strings)
  human_curated              — manually contributed (legacy; pre-harvest)
```

Sources outside this set are rejected at validation time. Adding a new source
class requires a spec amendment + a corresponding `tools/catalog-harvest/`
extractor.

## Validation rules

```
RULE-CATALOG-HARVEST-01: every entry under profiles-pending/ MUST pass
  schema_v1 validation (existing RULE-HWDB-01..09 chain).

RULE-CATALOG-HARVEST-02: source_class MUST be in the closed set above.

RULE-CATALOG-HARVEST-03: provenance.confidence MUST be in [0.0, 1.0]
  and MUST be > 0.5. Agents that can't justify > 0.5 should not emit
  the entry at all — under-confident harvest is just noise.

RULE-CATALOG-HARVEST-04: provenance.evidence MUST be non-empty and each
  entry MUST be ≤ 240 chars. Reviewer-readable; not a place to dump
  whole pages. Agents that can't extract pithy evidence are filing too
  speculatively.

RULE-CATALOG-HARVEST-05: harvested entries MUST NOT override an existing
  catalog entry with contributed_by != "harvest:*". Hand-curated entries
  win over harvested ones; the merge tool surfaces conflicts as comments
  on the PR for human resolution.

RULE-CATALOG-HARVEST-06: PII filter rejects entries whose evidence
  contains an email address, phone number, IP address, or 6+ digit
  numeric run that could be a serial number or order ID. Agents
  should not be extracting from purchase-record pages anyway, but
  defense in depth.

RULE-CATALOG-HARVEST-07: every entry's `captured_at` MUST be ≤ 30 days
  old at merge time. Stale harvest reflects pages that may have changed
  since extraction; the entry should be re-harvested, not stale-merged.
```

Bindings deferred to the implementation PR (rulelint will flag missing
bindings as it does for every other rule pack).

## Rollout

**Phase 0 (this spec PR)**: spec lands. No code changes; no harvest runs yet.
Reviewers can comment on the schema / source-class set / validation rules
before any extractor is written.

**Phase 1 (PR T-CATALOG-1)**: `tools/catalog-harvest/manufacturer_product_page/`
ships with extractors for ASUS, MSI, Gigabyte (the three vendors covering
~70% of consumer-DIY motherboards). Initial harvest run produces ~50 boards
under `profiles-pending/`. Manual review opens the first auto-PR.

**Phase 2 (PR T-CATALOG-2)**: `tools/catalog-merge/` ships — validates,
diffs, opens PR. Cron not yet wired; manual `make harvest-merge` invocation.

**Phase 3 (PR T-CATALOG-3)**: cron wiring. Weekly harvest runs in CI.
PR auto-opens; humans review + merge.

**Phase 4 (post-v0.6.0)**: photo-database integration (board photos with
header-position annotations). Out of scope for v0.6.0.

## Acceptance for v0.6.0 launch

- This spec merged.
- Phase 1 + Phase 2 PRs landed.
- ≥ 50 boards under `profiles-pending/`, ≥ 25 reviewed + merged into
  `profiles-v1.yaml` ahead of the v0.6.0 tag.
- Phoenix's MSI Z690-A (the canonical HIL) shows friendly fan names
  ("CPU_FAN1", "CHA_FAN1", "PUMP_FAN") on `/hardware` and `/health`
  instead of `pwm1` / `pwm2` / `pwm3`.

## Future work (post-v0.6.0)

- Photo-database integration: board photos rendered on the doctor / hardware
  surfaces with header-position highlights when an operator clicks into a fan.
- BIOS dump extraction pipeline (Phase 0 stays out of binary handling; Phase
  4+ can land it).
- Per-fan max-RPM measurement automation (operator opts into a test sweep
  that publishes the result back to the catalog).
- Cross-validation: when two source classes disagree on a board's fan
  layout, surface as a higher-confidence contradiction warning to the
  reviewer rather than silently picking one.

## Why "harvest" over "user submissions"

Three reasons:

1. **Bias**: contribution-driven catalogs reflect who contributes (DIY
   builders with high-end hardware), not the population of operators
   (tons of pre-built mini-PCs, OEM laptops, server hosts). Harvest
   sweeps every vendor's product page uniformly.

2. **Latency**: a board released today should be in the catalog within a
   week, not "when someone happens to own one and contribute". Weekly
   harvest closes that window.

3. **Audit**: every harvested entry has a `source_url` + `evidence`
   block. A user submission carries only the submitter's name. The
   harvest path is more auditable, not less.

The trade-off: harvest depends on agent infrastructure (sub-agent prompt
tooling, fetch budget, rate limits). Today that's reasonable; future cost
optimisation is on the roadmap.
