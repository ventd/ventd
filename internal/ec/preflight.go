package ec

import "fmt"

// Available probes both transports and returns the first that opens
// cleanly. ec_sys is preferred (the kernel handles the OBF/IBF
// handshake internally; one syscall per byte vs four per byte for
// /dev/port). On both-failed, returns a wrapped *SetupFailures that
// names the cause for each transport.
//
// When the kernel's Lockdown LSM is in "integrity" or
// "confidentiality" mode (the default under Secure Boot on most
// distros), both userspace transports are blocked unconditionally by
// the kernel. Available() detects this BEFORE attempting either
// transport and returns ErrECLockdownActive (which also satisfies
// errors.Is(err, ErrECNotAvailable) so existing callers branch
// correctly). This is important for the doctor card: the
// remediation under lockdown is NOT "modprobe ec_sys
// write_support=1" — the kernel will refuse — but rather "install
// the matching signed kernel module" or "enroll a MOK so a DKMS
// module can load".
//
// Caller pattern:
//
//	t, err := ec.Available()
//	if err != nil {
//	    if errors.Is(err, ec.ErrECLockdownActive) {
//	        // lockdown-specific doctor card
//	        return
//	    }
//	    if errors.Is(err, ec.ErrECNotAvailable) {
//	        // generic transport-not-available doctor card
//	        return
//	    }
//	    // unexpected — log and move on
//	}
//	defer t.Close()
//	wrapped := ec.WithAllowlist(t, cfg.RegistersUsed())
func Available() (Transport, error) {
	if LockdownActive() {
		return nil, fmt.Errorf("%w: %w", ErrECLockdownActive, ErrECNotAvailable)
	}
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
