# RULE-SYSCLASS-06: Laptop EC handshake succeeds when RPM changes within 5s; fails cleanly on context cancel.

`ProbeECHandshake(ctx context.Context, pwmEnablePath, rpmPath string) (bool, error)` polls
`rpmPath` at 200ms intervals for up to `ecHandshakeTimeout` (5s). It captures the initial
RPM reading, writes `1` to `pwmEnablePath` to enable manual mode, then waits for the RPM
to change from the initial value. When the RPM changes, the function returns `(true, nil)`.
When `ctx` is cancelled before a change is observed, the function returns `(false, ctx.Err())`.
When the timeout elapses with no change, it returns `(false, nil)`. A successful handshake
confirms the EC acknowledges manual PWM control. A context-cancelled return propagates the
cause without leaking goroutines or blocking the caller.

Bound: internal/sysclass/sysclass_test.go:TestRULE_SYSCLASS_06_LaptopECHandshake
