// Package observation implements the passive observation log for ventd's
// smart-mode patch sequence. Writer appends per-tick controller records;
// Reader streams them for Layer B/C cold-start re-warming and R13 doctor.
//
// The schema is locked by ventd-passive-observation-log-schema.md.
// v0.5.4 ships the write/read infrastructure only; consumer logic lands in
// v0.5.7 (Layer B), v0.5.8 (Layer C), and v0.5.10 (doctor).
package observation

import "github.com/ventd/ventd/internal/state"

const (
	obsLogName    = "observations"
	kvNamespace   = "observation"
	kvClassPrefix = "channel_class" // KV key = channel_class/<id>

	// schemaVersion is the v0.5.4 emit value (RULE-OBS-SCHEMA-03).
	schemaVersion = uint16(1)
)

// controller_state enum values — schema doc §2.2 (RULE-OBS-SCHEMA-04).
const (
	ControllerState_COLD_START      uint8 = 0
	ControllerState_WARMING         uint8 = 1
	ControllerState_CONVERGED       uint8 = 2
	ControllerState_DRIFTING        uint8 = 3
	ControllerState_ABORTED         uint8 = 4
	ControllerState_MANUAL_OVERRIDE uint8 = 5
	ControllerState_MONITOR_ONLY    uint8 = 6
)

// event_flags bitmask — schema doc §2.3 (RULE-OBS-SCHEMA-05).
// Bits 13–31 are reserved; the writer MUST NOT emit them.
const (
	EventFlag_LAYER_A_HARD_CAP        uint32 = 1 << 0
	EventFlag_ENVELOPE_C_ABORT        uint32 = 1 << 1
	EventFlag_ENVELOPE_D_FALLBACK     uint32 = 1 << 2
	EventFlag_DRIFT_TRIPPED           uint32 = 1 << 3
	EventFlag_SATURATION_DETECTED     uint32 = 1 << 4
	EventFlag_STALL_WATCHDOG_FIRED    uint32 = 1 << 5
	EventFlag_IDLE_GATE_REFUSED       uint32 = 1 << 6
	EventFlag_R12_GLOBAL_GATE_OFF     uint32 = 1 << 7
	EventFlag_LAYER_C_SHARD_ACTIVATED uint32 = 1 << 8
	EventFlag_LAYER_C_SHARD_EVICTED   uint32 = 1 << 9
	EventFlag_R9_IDENT_CLASS_CHANGED  uint32 = 1 << 10
	EventFlag_SIGNATURE_PROMOTED      uint32 = 1 << 11
	EventFlag_SIGNATURE_RETIRED       uint32 = 1 << 12
)

// eventFlagReservedMask covers bits 13–31 that the writer must never emit.
const eventFlagReservedMask uint32 = ^uint32((1 << 13) - 1)

// slowClassDrivers maps driver names to R11 class=1 (slow, 1/60 Hz).
// All other drivers default to class=0 (fast, 0.5 Hz).
// Used by loadOrInferClassMap when KV has no entry for a channel (RULE-OBS-RATE-04).
var slowClassDrivers = map[string]bool{
	"drivetemp": true,
	"bmc":       true,
	"ipmi":      true,
}

// DefaultRotationPolicy is the locked rotation policy (schema doc §4.2).
// MaxSizeMB=50 guards against runaway volume between midnight boundaries.
// MaxAgeDays=7 and KeepCount=8 give 7 days of daily retention plus one
// active file. CompressOld=true gzip-compresses files >10 MB on rotation
// per spec-16 §6.2.
var DefaultRotationPolicy = state.RotationPolicy{
	MaxSizeMB:   50,
	MaxAgeDays:  7,
	KeepCount:   8,
	CompressOld: true,
}
