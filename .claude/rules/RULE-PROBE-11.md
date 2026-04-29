# RULE-PROBE-11

The ventd daemon MUST NOT exit fatally on a persisted
`OutcomeRefuse` from `probe.LoadWizardOutcome`. Refuse is the
contract under which the first-run wizard explains why control is
unavailable; exiting bypasses that surface and leaves the operator
with no diagnostic UI.

On startup-time refuse, the daemon MUST:
- Log the refuse outcome and reason at WARN level.
- Continue startup so the web server binds and serves the
  setup / dashboard surfaces.

This rule complements RULE-PROBE-08 (which gates *wizard
behaviour* on the outcome). RULE-PROBE-08 governs the wizard;
RULE-PROBE-11 governs the daemon.

Bound: internal/probe/persist_test.go:TestRULE_PROBE_11_RefuseDoesNotBlockStartup
