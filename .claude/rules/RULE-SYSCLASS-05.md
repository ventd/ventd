# RULE-SYSCLASS-05: ClassServer + BMC-present systems require --allow-server-probe to proceed with Envelope C.

`ServerProbeAllowed(cls SystemClass, bmcPresent, allowServerProbe bool) bool` MUST return
`false` when `cls == ClassServer && bmcPresent && !allowServerProbe`. For all other
combinations — non-Server class, no BMC, or allowServerProbe=true — it returns `true`.
The Envelope C orchestrator calls this gate before entering the calibration loop. A server
with a BMC (detected via `/dev/ipmi*` or dmidecode type 38) may have BIOS-managed fan
curves that conflict with direct PWM writes; the operator must explicitly pass
`--allow-server-probe` to acknowledge the risk. On server hardware without a BMC
(e.g. a rack workstation without IPMI), the gate opens automatically.

Bound: internal/sysclass/sysclass_test.go:TestRULE_SYSCLASS_05_ServerBMCGate
