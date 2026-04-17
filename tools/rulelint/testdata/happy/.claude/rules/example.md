# Safety Rules

## RULE-CLAMP-01: PWM writes are clamped to [min_pwm, max_pwm]

Clamping prevents the curve from stalling a fan below its floor.

Bound: pkg/somefile_test.go:clamp_below_min

## RULE-STOP-01: PWM=0 requires allow_stop=true

Never stop a fan without an explicit opt-in.

Bound: pkg/somefile_test.go:stop_disabled
