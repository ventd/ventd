package sysclass

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ventd/ventd/internal/state"
)

const (
	kvNamespace     = "sysclass"
	kvKeyDetection  = "detection"
	kvSchemaVersion = uint16(1)
)

// kvDetection is the on-disk shape persisted under sysclass.detection.
type kvDetection struct {
	SchemaVersion uint16    `json:"schema_version"`
	Class         string    `json:"class"`
	Evidence      []string  `json:"evidence"`
	Tjmax         float64   `json:"tjmax"`
	Ambient       kvAmbient `json:"ambient"`
	BMCPresent    bool      `json:"bmc_present"`
	ECHandshakeOK *bool     `json:"ec_handshake_ok"`
	DetectedAt    time.Time `json:"detected_at"`
}

type kvAmbient struct {
	Source      string  `json:"source"`
	SensorPath  string  `json:"sensor_path,omitempty"`
	SensorLabel string  `json:"sensor_label,omitempty"`
	Reading     float64 `json:"reading"`
}

// PersistDetection writes the Detection to the spec-16 KV store under the
// `sysclass` namespace. Callers MUST invoke this before any Envelope C PWM
// write (RULE-SYSCLASS-02).
func PersistDetection(db *state.KVDB, d *Detection) error {
	rec := kvDetection{
		SchemaVersion: kvSchemaVersion,
		Class:         d.Class.String(),
		Evidence:      d.Evidence,
		Tjmax:         d.Tjmax,
		Ambient: kvAmbient{
			Source:      ambientSourceString(d.AmbientSensor.Source),
			SensorPath:  d.AmbientSensor.SensorPath,
			SensorLabel: d.AmbientSensor.SensorLabel,
			Reading:     d.AmbientSensor.Reading,
		},
		BMCPresent:    d.BMCPresent,
		ECHandshakeOK: d.ECHandshakeOK,
		DetectedAt:    time.Now().UTC(),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("sysclass: marshal detection: %w", err)
	}
	if err := db.Set(kvNamespace, kvKeyDetection, string(data)); err != nil {
		return fmt.Errorf("sysclass: persist detection: %w", err)
	}
	return nil
}

// LoadDetection retrieves a previously persisted Detection from the KV store.
// Returns (nil, false, nil) when no detection has been persisted yet.
func LoadDetection(db *state.KVDB) (*Detection, bool, error) {
	val, ok, err := db.Get(kvNamespace, kvKeyDetection)
	if err != nil {
		return nil, false, fmt.Errorf("sysclass: load detection: %w", err)
	}
	if !ok {
		return nil, false, nil
	}
	raw, ok := val.(string)
	if !ok {
		return nil, false, fmt.Errorf("sysclass: unexpected KV type for detection")
	}
	var rec kvDetection
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, false, fmt.Errorf("sysclass: unmarshal detection: %w", err)
	}
	d := &Detection{
		Class:         parseClass(rec.Class),
		Evidence:      rec.Evidence,
		Tjmax:         rec.Tjmax,
		BMCPresent:    rec.BMCPresent,
		ECHandshakeOK: rec.ECHandshakeOK,
		AmbientSensor: AmbientSensor{
			Source:      parseAmbientSource(rec.Ambient.Source),
			SensorPath:  rec.Ambient.SensorPath,
			SensorLabel: rec.Ambient.SensorLabel,
			Reading:     rec.Ambient.Reading,
		},
	}
	return d, true, nil
}

func ambientSourceString(s AmbientSource) string {
	switch s {
	case AmbientLabeled:
		return "labeled"
	case AmbientLowestAtIdle:
		return "lowest_at_idle"
	default:
		return "fallback_25c"
	}
}

func parseAmbientSource(s string) AmbientSource {
	switch s {
	case "labeled":
		return AmbientLabeled
	case "lowest_at_idle":
		return AmbientLowestAtIdle
	default:
		return AmbientFallback25C
	}
}

func parseClass(s string) SystemClass {
	switch s {
	case "hedt_air":
		return ClassHEDTAir
	case "hedt_aio":
		return ClassHEDTAIO
	case "mid_desktop":
		return ClassMidDesktop
	case "server":
		return ClassServer
	case "laptop":
		return ClassLaptop
	case "mini_pc":
		return ClassMiniPC
	case "nas_hdd":
		return ClassNASHDD
	default:
		return ClassUnknown
	}
}
