package polarity

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ventd/ventd/internal/probe"
)

// HwmonProber implements Prober for hwmon and EC channels that present
// sysfs-style pwm* and fan*_input files (spec §3.1).
type HwmonProber struct {
	// Clock is injectable for tests; defaults to time.Sleep.
	Clock func(time.Duration)
	// ReadFile reads a sysfs file; defaults to os.ReadFile.
	ReadFile func(string) ([]byte, error)
	// WriteFile writes a sysfs file; defaults to os.WriteFile.
	WriteFile func(string, []byte, os.FileMode) error
	// Now returns the current time; defaults to time.Now. Injected in tests
	// to control how many iterations readRPMMean executes.
	Now func() time.Time
}

func (p *HwmonProber) clock() func(time.Duration) {
	if p.Clock != nil {
		return p.Clock
	}
	return time.Sleep
}

func (p *HwmonProber) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *HwmonProber) readFile(path string) ([]byte, error) {
	if p.ReadFile != nil {
		return p.ReadFile(path)
	}
	return os.ReadFile(path)
}

func (p *HwmonProber) writeFile(path string, data []byte) error {
	if p.WriteFile != nil {
		return p.WriteFile(path, data, 0o644)
	}
	return os.WriteFile(path, data, 0o644)
}

// ProbeChannel implements Prober for hwmon/EC channels (RULE-POLARITY-01..04,
// RULE-POLARITY-13). Bipolar pulse: write BipolarLowPWM, settle, read RPM_low;
// write BipolarHighPWM, settle, read RPM_high. Classification delta is
// RPM_high − RPM_low — baseline-PWM-invariant. The pre-#1110 algorithm
// compared a single midpoint write against pre-write baseline RPM, which
// misclassified normal fans whose baseline PWM was already above midpoint:
// a fan running at PWM=255 / 2300 RPM slowed to ~1500 RPM under PWM=128,
// producing a false "inverted" label. Baseline PWM is captured for restore
// only — never used in classification.
func (p *HwmonProber) ProbeChannel(ctx context.Context, ch *probe.ControllableChannel) (ChannelResult, error) {
	res := ChannelResult{
		Backend:  "hwmon",
		Identity: Identity{PWMPath: ch.PWMPath, TachPath: ch.TachPath},
		Unit:     "rpm",
		ProbedAt: time.Now(),
	}

	if ch.TachPath == "" {
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonNoTach
		return res, nil
	}

	// Capture baseline PWM before any write so restore is exact.
	baselinePWMBytes, err := p.readFile(ch.PWMPath)
	if err != nil {
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonWriteFailed
		return res, nil
	}
	baselinePWMStr := strings.TrimSpace(string(baselinePWMBytes))
	baselinePWM, err := strconv.Atoi(baselinePWMStr)
	if err != nil {
		baselinePWM = 128 // safe fallback
	}

	// Restore is deferred; fires on every exit path (RULE-POLARITY-04).
	restored := false
	defer func() {
		if !restored {
			_ = p.writeFile(ch.PWMPath, []byte(strconv.Itoa(baselinePWM)+"\n"))
			p.clock()(RestoreDelay)
		}
	}()

	// Bipolar LOW pulse (RULE-POLARITY-13).
	if err := p.writeFile(ch.PWMPath, []byte(strconv.Itoa(int(BipolarLowPWM))+"\n")); err != nil {
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonWriteFailed
		return res, nil
	}
	select {
	case <-ctx.Done():
		return res, ctx.Err()
	default:
	}
	p.clock()(BipolarPulseHold)
	select {
	case <-ctx.Done():
		return res, ctx.Err()
	default:
	}
	rpmLow := p.readRPMMean(ch.TachPath, RestoreDelay)
	res.Baseline = rpmLow // Reused field: RPM observed at the LOW pulse.

	// Bipolar HIGH pulse (RULE-POLARITY-13).
	if err := p.writeFile(ch.PWMPath, []byte(strconv.Itoa(int(BipolarHighPWM))+"\n")); err != nil {
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonWriteFailed
		return res, nil
	}
	select {
	case <-ctx.Done():
		return res, ctx.Err()
	default:
	}
	p.clock()(BipolarPulseHold)
	select {
	case <-ctx.Done():
		return res, ctx.Err()
	default:
	}
	rpmHigh := p.readRPMMean(ch.TachPath, RestoreDelay)
	res.Observed = rpmHigh

	// Restore baseline before classifying.
	_ = p.writeFile(ch.PWMPath, []byte(strconv.Itoa(baselinePWM)+"\n"))
	p.clock()(RestoreDelay)
	restored = true

	// Classify on the bipolar delta — baseline-PWM-invariant.
	delta := rpmHigh - rpmLow
	res.Delta = delta

	switch {
	case math.Abs(delta) < ThresholdRPM:
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonNoResponse
	case delta > 0:
		res.Polarity = "normal"
	default:
		res.Polarity = "inverted"
	}
	return res, nil
}

// readRPMMean reads the tach file repeatedly over window and returns the mean.
// On read failure it returns 0.
func (p *HwmonProber) readRPMMean(tachPath string, window time.Duration) float64 {
	deadline := p.now().Add(window)
	var sum float64
	var count int
	for p.now().Before(deadline) {
		b, err := p.readFile(tachPath)
		if err != nil {
			break
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if err != nil {
			break
		}
		sum += v
		count++
		p.clock()(50 * time.Millisecond)
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// ProbeAll runs ProbeChannel on every hwmon/EC channel in channels sequentially.
// Results are written back into each channel and returned as a slice.
func (p *HwmonProber) ProbeAll(ctx context.Context, channels []*probe.ControllableChannel) ([]ChannelResult, error) {
	results := make([]ChannelResult, 0, len(channels))
	for _, ch := range channels {
		if ctx.Err() != nil {
			return results, fmt.Errorf("polarity probe: %w", ctx.Err())
		}
		res, err := p.ProbeChannel(ctx, ch)
		if err != nil && ctx.Err() != nil {
			return results, fmt.Errorf("polarity probe: %w", err)
		}
		ApplyToChannel(ch, res)
		results = append(results, res)
	}
	return results, nil
}
