package probe

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ventd/ventd/internal/state"
)

const (
	nsProbe       = "probe"
	nsWizard      = "wizard"
	nsCalibration = "calibration" // also wiped on reset (RULE-POLARITY-09)
)

// PersistOutcome writes the full ProbeResult and wizard outcome to the KV store
// (RULE-PROBE-07, RULE-PROBE-08). The result is JSON-encoded under probe.result;
// wizard.initial_outcome is set to the outcome string derived from r.
func PersistOutcome(db *state.KVDB, r *ProbeResult) error {
	if db == nil {
		return fmt.Errorf("probe persist: nil KVDB")
	}

	outcome := ClassifyOutcome(r)
	reason := OutcomeReason(r)

	resultJSON, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("probe persist: marshal result: %w", err)
	}

	return db.WithTransaction(func(tx *state.KVTx) error {
		now := time.Now().UTC().Format(time.RFC3339)

		tx.Set(nsProbe, "schema_version", 1)
		tx.Set(nsProbe, "last_run", now)
		tx.Set(nsProbe, "result", string(resultJSON))

		tx.Set(nsWizard, "initial_outcome", outcome.String())
		tx.Set(nsWizard, "outcome_reason", reason)
		tx.Set(nsWizard, "outcome_timestamp", now)
		return nil
	})
}

// LoadWizardOutcome reads the wizard.initial_outcome KV key and returns the
// parsed Outcome and whether the key was present. Used by daemon start to
// gate control mode (RULE-PROBE-08).
func LoadWizardOutcome(db *state.KVDB) (Outcome, bool, error) {
	if db == nil {
		return OutcomeControl, false, nil
	}

	v, ok, err := db.Get(nsWizard, "initial_outcome")
	if err != nil {
		return OutcomeControl, false, fmt.Errorf("probe: load wizard outcome: %w", err)
	}
	if !ok {
		return OutcomeControl, false, nil
	}

	s, _ := v.(string)
	switch s {
	case OutcomeControl.String():
		return OutcomeControl, true, nil
	case OutcomeMonitorOnly.String():
		return OutcomeMonitorOnly, true, nil
	case OutcomeRefuse.String():
		return OutcomeRefuse, true, nil
	default:
		return OutcomeControl, true, nil
	}
}

// WipeNamespaces deletes all keys under the wizard, probe, and calibration
// KV namespaces. Called by "Reset to initial setup" (RULE-PROBE-09,
// RULE-POLARITY-09).
func WipeNamespaces(db *state.KVDB) error {
	if db == nil {
		return nil
	}

	wizardKeys, err := db.List(nsWizard)
	if err != nil {
		return fmt.Errorf("probe wipe: list wizard: %w", err)
	}
	probeKeys, err := db.List(nsProbe)
	if err != nil {
		return fmt.Errorf("probe wipe: list probe: %w", err)
	}
	calibKeys, err := db.List(nsCalibration)
	if err != nil {
		return fmt.Errorf("probe wipe: list calibration: %w", err)
	}

	return db.WithTransaction(func(tx *state.KVTx) error {
		for k := range wizardKeys {
			tx.Delete(nsWizard, k)
		}
		for k := range probeKeys {
			tx.Delete(nsProbe, k)
		}
		for k := range calibKeys {
			tx.Delete(nsCalibration, k)
		}
		return nil
	})
}
