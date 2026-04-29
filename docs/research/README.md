# ventd research archive

Research and design artifacts that informed shipped specs and code.
Archived from claude.ai project knowledge 2026-04-29.

## Layout

- **`r-bundle/`** — Smart-mode research, R5 through R15. Inputs to
  `specs/spec-smart-mode.md`, `specs/spec-v0_5_2-polarity-disambiguation.md`,
  `specs/spec-16-persistent-state.md`, and the upcoming v0.5.4 passive
  observation log spec.

- **`hardware-detection/`** — Pre-v0.5.1 research on virtualisation /
  container detection (Tier-2), ghost hwmon entries, Steam Deck refusal
  strategy, firmware-locked vendors (Dell iDRAC9, HPE iLO, Supermicro AST),
  hidraw safety, and the broader hwmon survey. Inputs to spec-03
  amendments and the v0.5.1 catalog-less probe.

- **`board-catalog/`** — April 2026 research underlying spec-03 catalog
  scope-A/B/C entries, driver amendments, GPU vendor catalog, hwmon
  controllability map, and the userspace-fan-control integration survey
  (fan2go, LHM, NBFC, liquidctl).

- **`diagnostics/`** — Diag bundle privacy threat model (P1–P10
  redactor framework) and the diag bundle design document. Inputs to
  spec-03 PR 2c.

- **`predictive-thermal/`** — Pre-spec-05 predictive thermal control
  research. Superseded by `specs/spec-05-predictive-thermal.md` and the
  R15 audit; retained for design context.

## Status

These files are *design-of-record* for shipped or in-flight specs.
They are not active design surfaces — once a spec consumes the
research, the spec is the source of truth. Treat the research as
historical context, not as a requirements doc.

## Adding new research

Drop new design artifacts into the appropriate subfolder. If a new
category is needed (e.g. `controller-tuning/`), add it as a sibling
and update this README.

CC prompts (`cc-prompt-*.md`) are explicitly NOT archived — per
project rule, CC prompts are deleted post-ship.
