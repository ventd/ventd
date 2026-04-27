# RULE-POLARITY-10: All phantom reason codes are writable via WritePWM and return ErrChannelNotControllable.

Every defined `PhantomReason*` constant (`no_tach`, `no_response`, `firmware_locked`,
`profile_only`, `driver_too_old`, `write_failed`) represents a permanent non-controllable
state. A `ControllableChannel` with `Polarity="phantom"` and any `PhantomReason` value MUST
cause `WritePWM` to return `ErrChannelNotControllable` without calling `fn`. This is
verified exhaustively for all six reason codes so that adding a new reason code without
updating the write path cannot silently enable writes to an uncontrollable channel. The
reason code itself is advisory (shown in the setup wizard and `ventd doctor` output) and
does not affect the write refusal behaviour.

Bound: internal/polarity/polarity_test.go:TestPolarityRules/RULE-POLARITY-10_phantom_not_writable
