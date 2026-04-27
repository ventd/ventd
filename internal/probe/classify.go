package probe

// ClassifyOutcome derives the three-state wizard fork result from a ProbeResult
// following the algorithm in spec-v0_5_1 §3.2 (RULE-PROBE-04).
func ClassifyOutcome(r *ProbeResult) Outcome {
	if r.RuntimeEnvironment.Virtualised || r.RuntimeEnvironment.Containerised {
		return OutcomeRefuse
	}
	if len(r.ThermalSources) == 0 {
		return OutcomeRefuse
	}
	if len(r.ControllableChannels) == 0 {
		return OutcomeMonitorOnly
	}
	return OutcomeControl
}

// OutcomeReason returns a short machine-readable reason string for OutcomeRefuse
// or an empty string for other outcomes.
func OutcomeReason(r *ProbeResult) string {
	if r.RuntimeEnvironment.Virtualised {
		return "virtualised"
	}
	if r.RuntimeEnvironment.Containerised {
		return "containerised"
	}
	if len(r.ThermalSources) == 0 {
		return "no_sensors"
	}
	return ""
}
