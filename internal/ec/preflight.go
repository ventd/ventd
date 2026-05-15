package ec

import "fmt"

// Available probes both transports and returns the first that opens
// cleanly. ec_sys is preferred (the kernel handles the OBF/IBF
// handshake internally; one syscall per byte vs four per byte for
// /dev/port). On both-failed, returns a wrapped *SetupFailures that
// names the cause for each transport.
//
// Caller pattern:
//
//	t, err := ec.Available()
//	if err != nil {
//	    if errors.Is(err, ec.ErrECNotAvailable) {
//	        // surface doctor card; no fan control on this host
//	        return
//	    }
//	    // unexpected — log and move on
//	}
//	defer t.Close()
//	wrapped := ec.WithAllowlist(t, cfg.RegistersUsed())
func Available() (Transport, error) {
	tSys, errSys := openECSys()
	if errSys == nil {
		return tSys, nil
	}
	tPort, errPort := openDevPort()
	if errPort == nil {
		return tPort, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrECNotAvailable, (&SetupFailures{
		ECSys:   errSys,
		DevPort: errPort,
	}).Error())
}
