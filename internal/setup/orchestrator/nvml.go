package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ventd/ventd/internal/nvidia"
	"github.com/ventd/ventd/internal/recovery"
)

// NVMLGPUFan is one NVIDIA GPU fan visible via NVML. The address
// shape (Index uint) is the canonical NVML device handle the
// controller uses to write fan speeds.
type NVMLGPUFan struct {
	Index   uint   `json:"index"`            // NVML device index
	Label   string `json:"label"`            // user-facing "gpu0", "gpu1", …
	HasTemp bool   `json:"has_temp"`         // true when nvidia.ReadTemp succeeds
	TempC   int    `json:"temp_c,omitempty"` // current GPU temp at probe time
}

// NVMLArtifact is the structured result of the NVMLPhase. Consumed
// by ApplyPhase to add nvidia-type Fans to the config.
type NVMLArtifact struct {
	Available bool         `json:"available"` // true when nvidia.Init succeeded
	InitError string       `json:"init_error,omitempty"`
	Fans      []NVMLGPUFan `json:"fans,omitempty"`
}

// NVMLPhase initialises NVML and enumerates NVIDIA GPU fans. Always
// succeeds — a host with no NVIDIA hardware (or no GPU fans) produces
// an empty Fans list and Status:Success. The phase exists so the
// orchestrator's fan coverage matches the legacy wizard's Phase 3.
//
// NVML lifecycle is delicate (refcount-managed, can leak handles on
// repeated init/shutdown without pairing). The phase calls
// nvidia.Init then nvidia.Shutdown in the same Execute scope so the
// init/shutdown count balances; subsequent phases that need NVML
// (Polarity NVML prober, controller writes) Init again — the
// refcount infrastructure handles that.
type NVMLPhase struct{}

// Name identifies this phase in the checkpoint store and the wizard UI.
func (NVMLPhase) Name() string { return "nvml" }

// Execute runs nvidia.Init, counts GPUs, and emits an NVMLGPUFan per
// GPU with fans. Init failure is recorded but never fails the phase
// — a host without NVIDIA hardware is a legitimate state, not an
// error.
func (NVMLPhase) Execute(_ context.Context, rc *RunContext) Outcome {
	rc.Sink().Emit("info", "nvml", "initialising NVML (will be a no-op on non-NVIDIA hosts)")

	art := NVMLArtifact{}
	if err := nvidia.Init(rc.Log()); err != nil {
		art.InitError = err.Error()
		rc.Log().Info("nvml init declined (no NVIDIA fan control)", "err", err)
		raw, _ := EncodeArtifact(art)
		return Outcome{Status: StatusSuccess, Artifact: raw}
	}
	defer nvidia.Shutdown()
	art.Available = true

	count := nvidia.CountGPUs()
	for i := 0; i < count; i++ {
		idx := uint(i)
		if !nvidia.HasFans(idx) {
			continue
		}
		fan := NVMLGPUFan{
			Index: idx,
			Label: fmt.Sprintf("gpu%d", i),
		}
		if t, err := nvidia.ReadTemp(idx); err == nil {
			fan.HasTemp = true
			fan.TempC = int(t)
		}
		art.Fans = append(art.Fans, fan)
	}

	rc.Log().Info("nvml phase complete",
		"available", art.Available, "gpu_fans", len(art.Fans))

	raw, err := EncodeArtifact(art)
	if err != nil {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "encode artifact: " + err.Error(),
		}
	}
	return Outcome{Status: StatusSuccess, Artifact: raw}
}

// loadNVMLArtifact reads the NVMLPhase's checkpoint. Best-effort:
// missing/failed → ApplyPhase treats as "no NVIDIA fans" and skips
// the NVIDIA branch.
func loadNVMLArtifact(rc *RunContext) (NVMLArtifact, error) {
	store := NewCheckpointStore(rc.StateDir)
	state, err := store.Load()
	if err != nil {
		return NVMLArtifact{}, err
	}
	prior, ok := state.Outcomes[(NVMLPhase{}).Name()]
	if !ok {
		return NVMLArtifact{}, errors.New("NVMLPhase has not run")
	}
	if prior.Status != StatusSuccess && prior.Status != StatusSkipped {
		return NVMLArtifact{}, fmt.Errorf(
			"NVMLPhase did not succeed (status=%q)", prior.Status)
	}
	if len(prior.Artifact) == 0 {
		return NVMLArtifact{}, nil
	}
	var art NVMLArtifact
	if err := json.Unmarshal(prior.Artifact, &art); err != nil {
		return NVMLArtifact{}, fmt.Errorf("decode NVMLArtifact: %w", err)
	}
	return art, nil
}
