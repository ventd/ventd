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

// ProbeChannel implements Prober for hwmon/EC channels (RULE-POLARITY-01..04).
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

	// Measure baseline RPM over BaselineWindow.
	baselineRPM := p.readRPMMean(ch.TachPath, BaselineWindow)
	res.Baseline = baselineRPM

	// Write midpoint (RULE-POLARITY-01).
	if err := p.writeFile(ch.PWMPath, []byte("128\n")); err != nil {
		res.Polarity = "phantom"
		res.PhantomReason = PhantomReasonWriteFailed
		return res, nil
	}

	// Check for context cancellation before sleeping (RULE-POLARITY-04).
	select {
	case <-ctx.Done():
		return res, ctx.Err()
	default:
	}

	// Hold for 3 seconds (RULE-POLARITY-02).
	p.clock()(HoldDuration)

	select {
	case <-ctx.Done():
		return res, ctx.Err()
	default:
	}

	// Observe RPM over last 500ms of hold (RULE-POLARITY-01).
	observedRPM := p.readRPMMean(ch.TachPath, RestoreDelay)
	res.Observed = observedRPM

	// Restore baseline before classifying.
	_ = p.writeFile(ch.PWMPath, []byte(strconv.Itoa(baselinePWM)+"\n"))
	p.clock()(RestoreDelay)
	restored = true

	// Classify (RULE-POLARITY-03).
	delta := observedRPM - baselineRPM
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
