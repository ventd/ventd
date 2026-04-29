# RULE-IDLE-08: Backoff delay follows min(60×2^n, 3600) ± 20% jitter, with daily cap at n=12.

`BackoffDet(n int, randFloat func() float64) time.Duration` computes the retry delay for
the nth consecutive not-idle report. The base interval is `min(60s × 2^n, 3600s)`
(exponential backoff capped at 1 hour). Jitter is `±20%`: `delay × (1 + (randFloat()*2-1) × 0.2)`.
At `n ≥ 12` the function returns 0, signalling that the daily cap has been reached and
the caller should abandon the attempt for today. The test verifies: n=0 with zero-rand
gives `60s × 0.8 = 48s`; n=6 is capped to `3600s × 0.8`; n=12 returns 0; and the upper
jitter bound at n=0 with near-max rand does not exceed `60s × 1.2`. The daily cap
prevents a permanently busy system from hammering the process scanner on every tick.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_08_BackoffFormula
