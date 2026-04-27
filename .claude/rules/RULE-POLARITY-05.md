# RULE-POLARITY-05: WritePWM refuses writes to phantom channels (ErrChannelNotControllable) and unknown channels (ErrPolarityNotResolved).

`WritePWM(ch *probe.ControllableChannel, value uint8, fn func(uint8) error)` is the
polarity-aware write helper (spec §3.4). It dispatches on a closed set:
- `"normal"` → forwards `value` to `fn` unchanged
- `"inverted"` → forwards `255-value` to `fn`
- `"phantom"` → returns `ErrChannelNotControllable` without calling `fn`
- `"unknown"` → returns `ErrPolarityNotResolved` without calling `fn`
- any other value → returns a descriptive format error

A write to a phantom channel would attempt PWM manipulation on a channel backed by no
physical fan; a write to an unknown channel would proceed without inversion when the fan
may be inverted. Both are incorrect.

Bound: internal/polarity/polarity_test.go:TestPolarityRules/RULE-POLARITY-05_write_helper_refuses_phantom_unknown
