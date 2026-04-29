package envelope

import "time"

// ChannelState is the 9-state machine value stored in KV.
type ChannelState string

const (
	StateIdle           ChannelState = "idle"
	StateProbing        ChannelState = "probing"
	StatePausedUserIdle ChannelState = "paused_user_idle"
	StatePausedThermal  ChannelState = "paused_thermal"
	StatePausedLoad     ChannelState = "paused_load"
	StateCompleteC      ChannelState = "complete_C"
	StateAbortedC       ChannelState = "aborted_C"
	StateProbingD       ChannelState = "probing_D"
	StateCompleteD      ChannelState = "complete_D"
)

// Envelope identifies which probe envelope produced the result.
type Envelope string

const (
	EnvelopeC Envelope = "C"
	EnvelopeD Envelope = "D"
)

// EventType classifies each LogStore step event.
type EventType string

const (
	EventProbeStart    EventType = "probe_start"
	EventStepBegin     EventType = "step_begin"
	EventStepEnd       EventType = "step_end"
	EventProbePause    EventType = "probe_pause"
	EventProbeResume   EventType = "probe_resume"
	EventProbeAbort    EventType = "probe_abort"
	EventProbeComplete EventType = "probe_complete"
)

// EventFlags bit definitions.
const (
	FlagEnvelopeCAbort    uint32 = 1 << 4
	FlagEnvelopeDFallback uint32 = 1 << 5
	FlagIdleGateRefused   uint32 = 1 << 6
	FlagBIOSOverride      uint32 = 1 << 7
)

// StepEvent is the msgpack payload written to the LogStore for each probe event.
type StepEvent struct {
	SchemaVersion   int                `msgpack:"schema_version"`
	ChannelID       string             `msgpack:"channel_id"`
	Envelope        Envelope           `msgpack:"envelope"`
	EventType       EventType          `msgpack:"event_type"`
	TimestampNs     int64              `msgpack:"timestamp_ns"`
	PWMTarget       uint16             `msgpack:"pwm_target"`
	PWMActual       uint16             `msgpack:"pwm_actual"`
	Temps           map[string]float64 `msgpack:"temps"`
	RPM             uint32             `msgpack:"rpm"`
	ControllerState int                `msgpack:"controller_state"`
	EventFlags      uint32             `msgpack:"event_flags"`
	AbortReason     string             `msgpack:"abort_reason"`
}

// ChannelKV is the per-channel state persisted under calibration.envelope.<channel_id>.
type ChannelKV struct {
	State                   ChannelState `json:"state"`
	Envelope                Envelope     `json:"envelope"`
	StartedAt               time.Time    `json:"started_at"`
	CompletedStepCount      int          `json:"completed_step_count"`
	BaselinePWM             uint8        `json:"baseline_pwm"`
	LastStepPWM             uint8        `json:"last_step_pwm"`
	AbortReason             string       `json:"abort_reason"`
	LastUpdate              time.Time    `json:"last_update"`
	LastCalibrationEnvelope Envelope     `json:"last_calibration_envelope"`
}
