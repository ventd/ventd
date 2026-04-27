package polarity

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/ventd/ventd/internal/nvidia"
	"github.com/ventd/ventd/internal/probe"
)

const nvmlMinDriverMajor = 515

// NVMLProber implements Prober for NVML GPU fan channels (spec §3.2).
// It calls the nvidia package directly; tests inject a fake via NVMLFuncs.
type NVMLProber struct {
	// Clock is injectable for tests.
	Clock func(time.Duration)
	// NVMLFuncs allows test injection of NVML operations.
	NVMLFuncs NVMLInterface
}

// NVMLInterface abstracts the NVML operations needed for polarity probing.
// Production code uses nvmlReal{}; tests inject fakes.
type NVMLInterface interface {
	DriverVersion() (string, error)
	GetFanSpeed(index uint, fanIdx int) (uint8, error)
	GetFanControlPolicy(index uint, fanIdx int) (int, bool, error)
	SetFanControlPolicy(index uint, fanIdx int, policy int) (bool, error)
	SetFanSpeed(index uint, fanIdx int, pct uint8) error
}

// nvmlReal delegates to the nvidia package (production path).
type nvmlReal struct{}

func (nvmlReal) DriverVersion() (string, error) { return nvidia.DriverVersion() }
func (nvmlReal) GetFanSpeed(index uint, fanIdx int) (uint8, error) {
	// nvidia.ReadFanSpeed reads fanIdx=0; for polarity probe on fan 0 this is sufficient.
	// Future multi-fan support can extend nvidia.ReadFanSpeedN.
	_ = fanIdx
	return nvidia.ReadFanSpeed(index)
}
func (nvmlReal) GetFanControlPolicy(index uint, fanIdx int) (int, bool, error) {
	return nvidia.GetFanControlPolicy(index, fanIdx)
}
func (nvmlReal) SetFanControlPolicy(index uint, fanIdx int, policy int) (bool, error) {
	return nvidia.SetFanControlPolicy(index, fanIdx, policy)
}
func (nvmlReal) SetFanSpeed(index uint, fanIdx int, pct uint8) error {
	return nvidia.WriteFanSpeed(index, pct)
}

func (p *NVMLProber) clock() func(time.Duration) {
	if p.Clock != nil {
		return p.Clock
	}
	return time.Sleep
}

func (p *NVMLProber) nvml() NVMLInterface {
	if p.NVMLFuncs != nil {
		return p.NVMLFuncs
	}
	return nvmlReal{}
}

// NVMLChannelID encodes the GPU index and fan index for an NVML channel.
// PWMPath is formatted as "nvml:<gpuIndex>:<fanIndex>" by the probe layer.
type NVMLChannelID struct {
	GPUIndex uint
	FanIndex int
}

// ParseNVMLChannelID parses the PWMPath "nvml:<gpu>:<fan>" format.
func ParseNVMLChannelID(pwmPath string) (NVMLChannelID, error) {
	// PWMPath for NVML channels is "nvml:<gpuIndex>:<fanIndex>".
	parts := strings.Split(strings.TrimPrefix(pwmPath, "nvml:"), ":")
	if len(parts) != 2 {
		return NVMLChannelID{}, fmt.Errorf("polarity: invalid NVML channel path %q", pwmPath)
	}
	gpu, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return NVMLChannelID{}, fmt.Errorf("polarity: parse GPU index %q: %w", parts[0], err)
	}
	fan, err := strconv.Atoi(parts[1])
	if err != nil {
		return NVMLChannelID{}, fmt.Errorf("polarity: parse fan index %q: %w", parts[1], err)
	}
	return NVMLChannelID{GPUIndex: uint(gpu), FanIndex: fan}, nil
}

// ProbeChannel implements Prober for NVML channels (RULE-POLARITY-01..04, 06).
func (p *NVMLProber) ProbeChannel(ctx context.Context, ch *probe.ControllableChannel) (ChannelResult, error) {
	res := ChannelResult{
		Backend:  "nvml",
		Identity: Identity{PWMPath: ch.PWMPath},
		Unit:     "pct",
		ProbedAt: time.Now(),
	}

	id, err := ParseNVMLChannelID(ch.PWMPath)
	if err != nil {
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonWriteFailed
		return res, nil
	}
	res.Identity.FanIndex = id.FanIndex

	// RULE-POLARITY-06: gate on driver version ≥ R515.
	driverVer, err := p.nvml().DriverVersion()
	if err != nil {
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonDriverTooOld
		return res, nil
	}
	major := parseDriverMajor(driverVer)
	if major < nvmlMinDriverMajor {
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonDriverTooOld
		return res, nil
	}

	// Read baseline state (policy + speed).
	baselineSpeed, err := p.nvml().GetFanSpeed(id.GPUIndex, id.FanIndex)
	if err != nil {
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonWriteFailed
		return res, nil
	}
	baselinePolicy, hasPolicy, _ := p.nvml().GetFanControlPolicy(id.GPUIndex, id.FanIndex)
	res.Baseline = float64(baselineSpeed)

	// Restore is deferred on every path (RULE-POLARITY-04).
	restored := false
	defer func() {
		if !restored {
			_ = p.nvml().SetFanSpeed(id.GPUIndex, id.FanIndex, baselineSpeed)
			if hasPolicy {
				_, _ = p.nvml().SetFanControlPolicy(id.GPUIndex, id.FanIndex, baselinePolicy)
			}
			p.clock()(RestoreDelay)
		}
	}()

	// Set manual policy + 50% speed (RULE-POLARITY-01).
	if _, err := p.nvml().SetFanControlPolicy(id.GPUIndex, id.FanIndex, nvidia.FanPolicyTemperatureDiscrete); err != nil {
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonWriteFailed
		return res, nil
	}
	if err := p.nvml().SetFanSpeed(id.GPUIndex, id.FanIndex, 50); err != nil {
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonWriteFailed
		return res, nil
	}

	select {
	case <-ctx.Done():
		return res, ctx.Err()
	default:
	}

	// Hold 3 seconds (RULE-POLARITY-02).
	p.clock()(HoldDuration)

	select {
	case <-ctx.Done():
		return res, ctx.Err()
	default:
	}

	observedSpeed, err := p.nvml().GetFanSpeed(id.GPUIndex, id.FanIndex)
	if err != nil {
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonNoResponse
		return res, nil
	}
	res.Observed = float64(observedSpeed)

	// Restore.
	_ = p.nvml().SetFanSpeed(id.GPUIndex, id.FanIndex, baselineSpeed)
	if hasPolicy {
		_, _ = p.nvml().SetFanControlPolicy(id.GPUIndex, id.FanIndex, baselinePolicy)
	}
	p.clock()(RestoreDelay)
	restored = true

	// Classify (RULE-POLARITY-03).
	delta := float64(observedSpeed) - float64(baselineSpeed)
	res.Delta = delta

	switch {
	case math.Abs(delta) < ThresholdPct:
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonNoResponse
	case delta > 0:
		res.Polarity = "normal"
	default:
		res.Polarity = "inverted"
	}
	return res, nil
}

// parseDriverMajor returns the integer major version from a string like "570.211.01".
// Returns 0 on parse failure.
func parseDriverMajor(version string) int {
	parts := strings.SplitN(version, ".", 2)
	if len(parts) == 0 {
		return 0
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	return n
}
