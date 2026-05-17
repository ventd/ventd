# Sign-guard rules (v0.5.8)

These invariants govern v0.5.8's wrong-direction Layer-B prior
detector (`internal/coupling/signguard/`). Per R27, signguard
consumes the v0.5.5 opportunistic-probe observation-record
stream and votes on whether each channel's b_ii sign matches
the expected polarity for a cooling fan.

A confirmed channel is allowed to seed Layer-C from Layer-B's
b_ii prior; an unconfirmed channel admits with θ = [0, 0]
instead. Continuous detection (not warmup-only) catches a fan
re-cabled to an inverted-polarity header mid-deployment.

The patch spec is `specs/spec-v0_5_8-marginal-benefit.md` §2.5.
The motivating research is the v0.5.8 review's R27 finding (in
the spec's §8 follow-up table).

## RULE-SGD-VOTE-01: Sign vote requires ≥5 of last 7 opportunistic-probe samples agreeing.

Window size 7, threshold 5. Bounded false-positive rate ~2%
under independent noisy signs at p=0.5 (binomial CDF). Test
feeds 5 agreeing + 2 disagreeing samples and asserts Confirmed
returns true; with 4 + 3, false.

Bound: internal/coupling/signguard/signguard_test.go:TestSignVote_5Of7Threshold

## RULE-SGD-NOISE-01: Probes with |ΔT| < 2 °C (R11 noise floor) are discarded — no vote cast.

R11 §0's noise floor is the unambiguous detection threshold for
any hwmon temperature sensor. Probes whose ΔT magnitude falls
below this are uninformative and would inflate the false-vote
rate. Add returns false for sub-noise samples.

Bound: internal/coupling/signguard/signguard_test.go:TestSignVote_DiscardsBelowNoise

## RULE-SGD-CONT-01: signguard runs continuously, not warmup-only — confirmed→unconfirmed downgrade is supported.

A re-cabled fan that flips polarity mid-deployment must be
caught at any point in daemon lifetime. The rolling 7-sample
window naturally supports downgrade: 5 disagreeing samples
after confirmation pull the agreement count below threshold,
returning Confirmed=false on the next call.

Bound: internal/coupling/signguard/signguard_test.go:TestSignVote_DowngradeOnFlipMidLifetime
