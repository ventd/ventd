# RULE-ENVELOPE-01: All PWM writes during envelope probing MUST go through polarity.WritePWM — never direct sysfs writes.

The envelope prober owns a `channelWriter` per channel. `channelWriter.Write(value uint8)` calls
`polarity.WritePWM(ch, value, writeFunc)` which enforces the polarity correction and phantom/unknown
channel guards. Any code path that writes directly to the sysfs PWM file (bypassing `polarity.WritePWM`)
violates the inverted-channel contract: the fan runs in the wrong direction and every RPM/temperature
reading produced during the probe is meaningless. The test injects a phantom and an unknown channel
and asserts that Write returns ErrChannelNotControllable and ErrPolarityNotResolved respectively,
with zero bytes written to the underlying writeFunc.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_01_WritePWMViaHelper
