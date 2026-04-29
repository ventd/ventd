# RULE-POLARITY-07: IPMI polarity probe uses a vendor-dispatch interface; Dell firmware-locked and HPE profile-only channels are permanent phantom.

`IPMIVendorProbe` is an interface with `ProbeIPMIPolarity(ctx, ch) (ChannelResult, error)`.
Three implementations are shipped:

- `SupermicroIPMIProbe`: writes an OEM SET_FAN_SPEED command and reads the SDR tach
  back; classifies by RPM delta as per RULE-POLARITY-03.
- `DellIPMIProbe`: attempts a write; a non-zero completion code (CC) from iDRAC9 ≥ 3.34
  (`firmware_locked`) returns `Polarity="phantom"`, `PhantomReason=PhantomReasonFirmwareLocked`
  without further probing.
- `HPEIPMIProbe`: always returns `Polarity="phantom"`, `PhantomReason=PhantomReasonProfileOnly`
  because HPE 405/501 exposes only the iLO Advanced profile interface, not direct fan writes.

Permanent phantom channels are excluded from the polarity-aware write path for the lifetime
of the daemon without re-probing.

Bound: internal/polarity/polarity_test.go:TestPolarityRules/RULE-POLARITY-07_ipmi_vendor_probe_interface
