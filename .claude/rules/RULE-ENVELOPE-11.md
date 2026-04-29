# RULE-ENVELOPE-11: Channels are probed sequentially — never concurrently.

`Prober.Probe(ctx, channels)` iterates over channels in index order, fully completing
(or aborting) each channel's Envelope C/D probe before advancing to the next. No goroutine
is spawned per channel. Concurrent probing is forbidden because simultaneous PWM writes on
multiple channels produce interfering thermal gradients that invalidate the steady-state RPM
readings used to determine the envelope curve. The test injects three channels and records
the sequence of write calls; it asserts that all writes for channel 0 precede all writes for
channel 1, which precede all writes for channel 2 — no interleaving.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_11_SequentialChannelsNoParallel
