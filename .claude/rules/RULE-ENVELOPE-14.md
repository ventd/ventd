# RULE-ENVELOPE-14: PWM readback after each step write must match the written value within ±2 LSB.

After `cw.Write(stepValue)` returns nil, `readPWM(ch.PWMPath)` MUST be called and the
readback compared to `stepValue`. A discrepancy of more than 2 LSB indicates BIOS override
or driver rounding and MUST cause the step to be logged with `EventFlags |= FlagBIOSOverride`
and the channel's abort path to be triggered. The ±2 LSB tolerance accommodates drivers that
round to even values (nct6775 rounds pwm to multiples of 2.56, equivalent to ±1 after integer
truncation). A readback that silently diverges by more than 2 LSB means calibration continues
on data that does not reflect actual fan response.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_14_PWMReadbackVerification
