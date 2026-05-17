package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/polarity"
	"github.com/ventd/ventd/internal/probe"
)

// fakePolarityProber returns per-PWMPath configured results. The
// channels-tested slice records the order ProbeChannel was called for
// ordering assertions.
type fakePolarityProber struct {
	results        map[string]polarity.ChannelResult
	errors         map[string]error
	channelsTested []string
}

func (f *fakePolarityProber) ProbeChannel(_ context.Context, ch *probe.ControllableChannel) (polarity.ChannelResult, error) {
	f.channelsTested = append(f.channelsTested, ch.PWMPath)
	if err, ok := f.errors[ch.PWMPath]; ok {
		return polarity.ChannelResult{}, err
	}
	r, ok := f.results[ch.PWMPath]
	if !ok {
		return polarity.ChannelResult{Polarity: "normal", ProbedAt: time.Now()}, nil
	}
	return r, nil
}

// seedProbeCheckpoint writes a ProbeArtifact under the state dir so
// PolarityPhase / ApplyPhase have something to consume.
func seedProbeCheckpoint(t *testing.T, rc *RunContext, art ProbeArtifact) {
	t.Helper()
	store := NewCheckpointStore(rc.StateDir)
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(art)
	state.Outcomes[(ProbePhase{}).Name()] = Outcome{
		Phase:    (ProbePhase{}).Name(),
		Status:   StatusSuccess,
		Artifact: raw,
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
}

func TestPolarityPhase_Name(t *testing.T) {
	if (PolarityPhase{}).Name() != "polarity" {
		t.Error("Name() must be 'polarity'")
	}
}

func TestPolarityPhase_ProbesEveryFanFromProbeArtifact(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: "/sys/hwmon0/pwm1", ChipName: "nct6687"},
			{Index: 2, PWMPath: "/sys/hwmon0/pwm2", ChipName: "nct6687"},
		},
	})
	prober := &fakePolarityProber{}
	out := (PolarityPhase{Prober: prober}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("status=%q detail=%q", out.Status, out.Detail)
	}
	if len(prober.channelsTested) != 2 {
		t.Errorf("expected 2 fans probed, got %d", len(prober.channelsTested))
	}
}

func TestPolarityPhase_CapturesPhantomReason(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{{Index: 1, PWMPath: "/sys/hwmon0/pwm1"}},
	})
	prober := &fakePolarityProber{
		results: map[string]polarity.ChannelResult{
			"/sys/hwmon0/pwm1": {
				Polarity: "phantom", PhantomReason: "no_rpm_response",
				Baseline: 0, Observed: 0, Delta: 0,
			},
		},
	}
	out := (PolarityPhase{Prober: prober}).Execute(context.Background(), rc)
	var art PolarityArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Results) != 1 || art.Results[0].Polarity != "phantom" {
		t.Errorf("phantom polarity lost in artifact: %+v", art)
	}
	if art.Results[0].PhantomReason != "no_rpm_response" {
		t.Errorf("PhantomReason lost: %q", art.Results[0].PhantomReason)
	}
}

func TestPolarityPhase_RecordsProberErrorPerChannel(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{
		Fans: []ProbedFan{
			{Index: 1, PWMPath: "/sys/hwmon0/pwm1"},
			{Index: 2, PWMPath: "/sys/hwmon0/pwm2"},
		},
	})
	prober := &fakePolarityProber{
		errors: map[string]error{
			"/sys/hwmon0/pwm1": errors.New("driver rejected mode flip"),
		},
		results: map[string]polarity.ChannelResult{
			"/sys/hwmon0/pwm2": {Polarity: "normal"},
		},
	}
	out := (PolarityPhase{Prober: prober}).Execute(context.Background(), rc)
	if out.Status != StatusSuccess {
		t.Fatalf("per-fan errors should not fail the phase, got %q", out.Status)
	}
	var art PolarityArtifact
	_ = json.Unmarshal(out.Artifact, &art)
	if len(art.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(art.Results))
	}
	if art.Results[0].ProbeError == "" {
		t.Error("Results[0].ProbeError should be populated for the failing fan")
	}
	if art.Results[1].Polarity != "normal" {
		t.Errorf("Results[1].Polarity = %q, want normal", art.Results[1].Polarity)
	}
}

func TestPolarityPhase_EmptyProbeArtifactSkips(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	seedProbeCheckpoint(t, rc, ProbeArtifact{Fans: nil})
	out := (PolarityPhase{Prober: &fakePolarityProber{}}).Execute(context.Background(), rc)
	if out.Status != StatusSkipped {
		t.Errorf("empty probe artifact should yield Skipped, got %q", out.Status)
	}
}

func TestPolarityPhase_MissingProbeArtifactFails(t *testing.T) {
	rc := &RunContext{StateDir: t.TempDir()}
	out := (PolarityPhase{Prober: &fakePolarityProber{}}).Execute(context.Background(), rc)
	if out.Status != StatusFailed {
		t.Errorf("missing prior ProbePhase should yield Failed, got %q", out.Status)
	}
}
