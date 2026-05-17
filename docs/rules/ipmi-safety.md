# IPMI Safety Rules

These invariants govern the IPMI backend that communicates with server BMCs
via raw ioctls on /dev/ipmi0. Violating them risks incorrect fan state,
unrecovered manual-mode after daemon exit, or silent failures on unsupported
hardware.

Each rule below is bound to one subtest in `internal/hal/ipmi/safety_test.go`.
If a rule text is edited, update the corresponding subtest in the same PR;
if a new rule lands, it must ship with a matching subtest or the rule-lint
in `tools/rulelint` blocks the merge.

## RULE-IPMI-1: Supermicro X11 happy path — enumerate, read, write, restore succeed

On a Supermicro X11 system the full control cycle must complete without error:
Enumerate walks the SDR and returns at least one fan channel; Read returns an
OK reading from Get Sensor Reading; Write sends the OEM SET_FAN_SPEED command
(netFn=0x30 cmd=0x70) with a valid completion code; Restore sends SET_FAN_MODE
(netFn=0x30 cmd=0x45) to return fans to firmware auto. Any step failing leaves
fans in an uncontrolled state.

Bound: internal/hal/ipmi/safety_test.go:supermicro_x11_happy_path

## RULE-IPMI-2: Dell PowerEdge R750 happy path — enumerate, read, write, restore succeed

On a Dell PowerEdge R750 the same full control cycle must complete: Enumerate
populates channels from the SDR; Read returns an OK reading; Write issues the
Dell OEM fan command (netFn=0x30 cmd=0x30 sub=0x02); Restore re-enables
automatic fan control via the same command with sub=0x01. The Dell path uses
different OEM bytes than Supermicro and must be independently verified.

Bound: internal/hal/ipmi/safety_test.go:dell_r750_happy_path

## RULE-IPMI-3: HPE iLO returns "iLO Advanced required" on write; no write attempted

When the detected vendor is HPE, any call to Write must return a non-nil error
whose message contains the substring "iLO Advanced". The backend must not
attempt any OEM ioctl — iLO requires a paid licence for fan control and sending
unsupported commands generates BMC error log entries. Restore must be a no-op
(nil error, no ioctl) because manual mode was never taken.

Bound: internal/hal/ipmi/safety_test.go:hpe_ilo_license_required

## RULE-IPMI-4: Unknown vendor refuses manual-mode write; returns structured error

When the vendor is not Supermicro, Dell, or HPE, Write must return a non-nil
error containing "unsupported vendor" and must not issue any BMC command. An
unknown-vendor machine passed DMI gating (rack chassis or known OEM string)
but has no safe OEM fan-write path; issuing a guess command could enable or
disable a built-in safety feature in an unpredictable way.

Bound: internal/hal/ipmi/safety_test.go:unknown_vendor_refuses_manual_mode

## RULE-IPMI-5: CC=0xC3 (node busy) surfaces as a clean error; next write succeeds

When the BMC returns completion code 0xC3 (IPMI_CC_NODE_BUSY), ioctlSendRecv
must propagate a non-nil error to the caller — never silently succeed. Once
the BMC is no longer busy, a subsequent Write to the same channel must succeed.
This tests that a busy response does not corrupt backend state (e.g. by
leaving the mutex held or the sequence counter in a bad state) that would
prevent future writes.

Bound: internal/hal/ipmi/safety_test.go:bmc_busy_retry_succeeds

## RULE-IPMI-6: BMC timeout surfaces as structured error with no goroutine leak

When the sendRecv transport returns an error (modelling a BMC that never
responds), Write must surface that error to the caller — it must not swallow
it, retry indefinitely, or spawn goroutines that outlive the call. Verified
with go.uber.org/goleak.VerifyNone to ensure the backend's synchronous
send-poll-receive loop leaves no background goroutines after an error path.

Bound: internal/hal/ipmi/safety_test.go:ioctl_timeout_no_goroutine_leak

## RULE-IPMI-7: Restore for every channel sends the vendor-specific auto-mode command

The watchdog exit path calls Restore once per registered channel. Each call
must issue exactly one vendor-specific "return to auto" command — Supermicro:
SET_FAN_MODE (netFn=0x30 cmd=0x45 data=[0x00]); Dell: auto-enable sub-command
(netFn=0x30 cmd=0x30 data=[0x01,0x01]). With N channels, exactly N restore
commands must be sent; a Restore that silently skips a channel leaves that fan
at the daemon's last written duty cycle after daemon exit.

Bound: internal/hal/ipmi/safety_test.go:restore_on_exit_all_channels
