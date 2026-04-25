# Install Contract Rules

These invariants guard against drift between what the shipped systemd unit
files promise and what the install artefacts actually deliver. Each drift
class was found in the v0.4.0 Proxmox smoke test: a user account referenced
but never declared, an OnFailure handler referencing a missing unit, a
web listener default that tripped the TLS safety check on first boot, and an
AppArmor profile directive with no backing profile on disk.

Each rule below is bound to one subtest in `deploy/install-contract_test.go`.
If a rule text is edited, update the corresponding subtest in the same PR;
if a new rule lands, it must ship with a matching subtest or the rule-lint
in `tools/rulelint` blocks the merge.

## RULE-INSTALL-01: Every User= directive in deploy/*.service must have a matching sysusers.d drop-in

For each `User=<name>` directive in a shipped unit file, a file
`deploy/sysusers.d-<name>.conf` must exist in the source tree and must
declare the named account with a `u <name>` line. A unit file that references
an account not created by a shipped drop-in will fail with systemd exit status
217/USER on any machine that does not already have the account — including
every fresh install. The test verifies both `User=ventd` (main daemon) and
`User=ventd-ipmi` (IPMI sidecar) resolve to their respective drop-ins.

Bound: deploy/install-contract_test.go:TestInstallContract_UserDeclared

## RULE-INSTALL-02: Every OnFailure= directive must reference a unit file present in deploy/

A `OnFailure=<unit>` line in a shipped service causes systemd to start
`<unit>` when the service exits with a failure code. If the referenced unit
does not exist on disk, systemd logs a cascading failure chain with no
recovery path. Every `OnFailure=` value in `deploy/*.service` must correspond
to a `.service` file also present in `deploy/`. The test reads the actual
`ventd.service` file and confirms each declared dependency exists.

Bound: deploy/install-contract_test.go:TestInstallContract_OnFailureResolves

## RULE-INSTALL-03: The web.listen default must not bind to 0.0.0.0 without TLS

The value returned by `config.Empty().Web.Listen` must pass
`Web.RequireTransportSecurity()` without error. A wildcard default
(`0.0.0.0:any-port`) without TLS configured causes `RequireTransportSecurity`
to refuse daemon startup on every fresh install — exactly the failure mode that
drove this spec. Loopback-only (`127.0.0.1`) is always permitted without TLS;
operators who need LAN access must explicitly set a non-loopback address
alongside a TLS configuration.

Bound: deploy/install-contract_test.go:TestInstallContract_WebListenDefault

## RULE-INSTALL-04: Every AppArmorProfile= directive must reference a profile shipped in deploy/apparmor.d/

A `AppArmorProfile=<name>` directive in a shipped unit pins the process to
the named AppArmor profile. If no profile file is present at
`deploy/apparmor.d/<name>`, systemd fails with exit status 231/APPARMOR on
AppArmor-enforcing distributions (Ubuntu, Debian). The test enumerates every
`AppArmorProfile=` value across `deploy/*.service` and asserts that a
corresponding file exists under `deploy/apparmor.d/`. This test is in skip
state until PR 2 ships the AppArmor profiles.

Bound: deploy/install-contract_test.go:TestInstallContract_AppArmorProfileShipped
