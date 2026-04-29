package sysclass

// AmbientBoundsOK returns ("", true) when reading is in [10, 50] °C.
// A reading below 10 °C returns "AMBIENT-IMPLAUSIBLE-TOO-COLD"; above 50 °C
// returns "AMBIENT-IMPLAUSIBLE-TOO-HOT". Callers (Envelope C) MUST refuse to
// proceed when ok is false (RULE-SYSCLASS-04).
func AmbientBoundsOK(reading float64) (code string, ok bool) {
	if reading < 10.0 {
		return "AMBIENT-IMPLAUSIBLE-TOO-COLD", false
	}
	if reading > 50.0 {
		return "AMBIENT-IMPLAUSIBLE-TOO-HOT", false
	}
	return "", true
}

// ServerProbeAllowed returns true when the system class may proceed with
// Envelope C calibration probes. A Class 4 server with a detected BMC
// requires allowServerProbe=true; unexpected Envelope C writes can
// conflict with BMC thermal management (RULE-SYSCLASS-05).
func ServerProbeAllowed(cls SystemClass, bmcPresent, allowServerProbe bool) bool {
	if cls != ClassServer {
		return true
	}
	if bmcPresent && !allowServerProbe {
		return false
	}
	return true
}
