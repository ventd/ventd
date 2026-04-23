---
name: spec-writer
description: Produces a markdown specification for a ventd feature or subsystem following the project's spec template in specs/. Use when the user says "write a spec for...", "draft a design doc for...", or asks to document a change before implementation.
when_to_use: Invoke before implementation. Do not invoke for bug reports or trivial changes.
argument-hint: [feature-short-name]
allowed-tools: Read Write Glob Grep
---

# Spec writer

Produce specs/YYYY-MM-DD-<feature-short-name>.md using today's date.

## Required sections in order

1. Spec title heading
2. Status — one of: Draft, Accepted, Implemented, Superseded
3. Context — 2-5 paragraphs, link to issues
4. Goals — bullets, each testable
5. Non-goals
6. Design — data structures (Go types), state machines (ASCII), failure modes, concurrency model
7. Alternatives considered — at least two, with rejection reasons
8. Migration / compatibility — on-disk formats, config, CLI
9. Testing strategy — unit, integration, fuzz, soak, HIL matrix
10. Open questions

## Style rules

- Present tense, imperative voice.
- No marketing language.
- Every Go identifier in backticks.
- Link to files with repo-relative paths.
- If a section does not apply, write N/A with a one-line reason. Never omit.

## Process

1. Glob specs/ to match voice of prior specs.
2. Read the referenced issue or code before drafting.
3. Ask at most one clarifying question, only if Context or Goals are genuinely ambiguous.
4. Write the file; do not print contents to chat.
5. Report: file path, word count, open-questions count.

## Invariants from ventd project

- Read .claude/rules/*.md before spec drafting if the spec touches watchdog, HAL contract, hwmon safety, or calibration. These contain 1:1 test-binding invariants that must be respected in the design.
- HARDWARE-REQUIRED sections must be explicit — list exact hardware needed to validate each DoD item.
- Every control-loop change references the relevant benchmark target and expected impact.
- Every spec affecting on-disk format, CLI surface, or config schema includes a migration section. No exceptions.
