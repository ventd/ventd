package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

// TestDeriveVerdict_WarmingUp_ZeroTheta covers the canonical "fresh
// install, observation log empty" case Phoenix's desktop saw on
// v0.5.26 — Layer-B shards persisted with theta=[0,0] and never
// converged because static-PWM workload didn't satisfy
// RULE-CMB-OAT-01's Δpwm-on-i-while-zero-on-j requirement.
func TestDeriveVerdict_WarmingUp_ZeroTheta(t *testing.T) {
	b := SoakBucket{
		SchemaVersion:    1,
		HwmonFingerprint: "abc123",
		ChannelID:        "/sys/class/hwmon/hwmon3/pwm1",
		NCoupled:         0,
		Theta:            []float64{0, 0},
		PSerialised:      pSerialiseDiag([]float64{50, 50}), // d=2, tr(P)=100=tr(P_0)
		Lambda:           0.99,
		NSamples:         0,
		LastSeenUnix:     0,
	}
	v := deriveVerdict(b)

	if v.D != 2 {
		t.Fatalf("expected d=2 (1+0+1), got %d", v.D)
	}
	if v.NSamplesGate != 20 {
		t.Errorf("expected n_samples_gate=20 (5·d²=5·4), got %d", v.NSamplesGate)
	}
	if !v.ThetaIsZero {
		t.Errorf("theta should be flagged as zero")
	}
	if v.NSamplesPasses {
		t.Errorf("n_samples_passes should be false at n=0")
	}
	if v.TrPPasses {
		t.Errorf("tr_p_passes should be false when tr(P)=tr(P_0)")
	}
	if got, want := math.Round(v.TrP*1000)/1000, 100.0; got != want {
		t.Errorf("tr(P): expected %.3f, got %.3f", want, got)
	}
}

// TestDeriveVerdict_Converged covers the post-excitation case the
// soak harness is built to detect: theta non-zero, n_samples cleared
// the 5·d² gate, tr(P) shrunk below 0.5·tr(P_0).
func TestDeriveVerdict_Converged(t *testing.T) {
	b := SoakBucket{
		SchemaVersion:    1,
		HwmonFingerprint: "abc123",
		ChannelID:        "/sys/class/hwmon/hwmon3/pwm2",
		NCoupled:         0,
		Theta:            []float64{0.85, -0.012},           // a=0.85, b_ii=-0.012 (cooling fan)
		PSerialised:      pSerialiseDiag([]float64{10, 10}), // tr(P)=20, gate is 50
		Lambda:           0.99,
		NSamples:         100, // well above 5·d²=20
		LastSeenUnix:     1715200000,
	}
	v := deriveVerdict(b)

	if v.ThetaIsZero {
		t.Errorf("theta should not be flagged as zero")
	}
	if !v.NSamplesPasses {
		t.Errorf("n_samples_passes: expected true at n=100, gate=20")
	}
	if !v.TrPPasses {
		t.Errorf("tr_p_passes: expected true when tr(P)=20 < 0.5·100=50")
	}
	if !strings.Contains(verdictText(v), "converged") {
		t.Errorf("verdictText should report converged, got %q", verdictText(v))
	}
}

// TestDeriveVerdict_NCoupled3 verifies the d-from-NCoupled formula
// with a non-trivial coupling count. d = 1 + 3 + 1 = 5; gate = 5·25 = 125.
func TestDeriveVerdict_NCoupled3(t *testing.T) {
	b := SoakBucket{
		SchemaVersion: 1,
		ChannelID:     "/sys/class/hwmon/hwmon3/pwm3",
		NCoupled:      3,
		Theta:         []float64{0.85, -0.01, -0.005, -0.005, -0.002},
		PSerialised:   pSerialiseDiag([]float64{10, 10, 10, 10, 10}),
		NSamples:      130,
	}
	v := deriveVerdict(b)
	if v.D != 5 {
		t.Errorf("expected d=5 (1+3+1), got %d", v.D)
	}
	if v.NSamplesGate != 125 {
		t.Errorf("expected n_samples_gate=125 (5·25), got %d", v.NSamplesGate)
	}
}

// TestTraceFromSerialised_Diagonal verifies the diagonal extraction
// from the upper-triangle layout produced by coupling.serialiseSymDense.
// For d=3 the upper triangle is 6 entries in row-major order:
// (0,0)(0,1)(0,2)(1,1)(1,2)(2,2). Off-diagonal values are intentionally
// large so a bug that summed them would inflate the trace.
func TestTraceFromSerialised_Diagonal(t *testing.T) {
	d := 3
	upper := []float64{
		1, 99, 99, // row 0: diag=1, off=99,99
		2, 99, // row 1: diag=2, off=99
		3, // row 2: diag=3
	}
	raw := pSerialise(upper)
	got := traceFromSerialised(raw, d)
	want := 6.0 // 1+2+3
	if got != want {
		t.Errorf("trace: expected %.1f, got %.3f (off-diagonal leak suspected)", want, got)
	}
}

// TestTraceFromSerialised_Malformed returns 0 on a too-short payload
// rather than panicking — operator sees tr_p_passes=false and
// investigates rather than the binary crashing on a corrupted shard.
func TestTraceFromSerialised_Malformed(t *testing.T) {
	if got := traceFromSerialised([]byte{1, 2, 3}, 2); got != 0 {
		t.Errorf("expected 0 on malformed input, got %.3f", got)
	}
}

// TestReadShards_NoShardDir handles the first-boot case where the
// daemon hasn't written any Layer-B state yet. Returns empty slice +
// nil error.
func TestReadShards_NoShardDir(t *testing.T) {
	stateDir := t.TempDir()
	verdicts, err := readShards(stateDir)
	if err != nil {
		t.Fatalf("readShards on empty dir: unexpected error %v", err)
	}
	if len(verdicts) != 0 {
		t.Errorf("expected 0 verdicts, got %d", len(verdicts))
	}
}

// TestReadShards_DecodesValidShard writes a known-good msgpack file
// to the conventional path and asserts readShards picks it up + maps
// fields correctly. This is the integration round-trip — exercises the
// decode path against a real on-disk file.
func TestReadShards_DecodesValidShard(t *testing.T) {
	stateDir := t.TempDir()
	shardDir := filepath.Join(stateDir, "smart", "shard-B")
	if err := os.MkdirAll(shardDir, 0o755); err != nil {
		t.Fatal(err)
	}

	b := SoakBucket{
		SchemaVersion:    1,
		HwmonFingerprint: "deadbeef",
		ChannelID:        "/sys/class/hwmon/hwmon3/pwm1",
		NCoupled:         0,
		Theta:            []float64{0.9, -0.015},
		PSerialised:      pSerialiseDiag([]float64{5, 5}),
		Lambda:           0.99,
		NSamples:         50,
		LastSeenUnix:     1715200000,
	}
	data, err := msgpack.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shardDir, "channel1.cbor"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	verdicts, err := readShards(stateDir)
	if err != nil {
		t.Fatalf("readShards: %v", err)
	}
	if len(verdicts) != 1 {
		t.Fatalf("expected 1 verdict, got %d", len(verdicts))
	}
	if verdicts[0].ChannelID != b.ChannelID {
		t.Errorf("channel mismatch: %q vs %q", verdicts[0].ChannelID, b.ChannelID)
	}
	if verdicts[0].HwmonFingerprint != b.HwmonFingerprint {
		t.Errorf("fingerprint mismatch")
	}
}

// TestEmitVerdicts_NDJSON_RoundTrip pins the JSON output format —
// operators pipe `ventd-soak snapshot --json` to per-host trace files
// across long watch runs and parse them with `jq`. A schema break would
// silently invalidate every parser downstream.
func TestEmitVerdicts_NDJSON_RoundTrip(t *testing.T) {
	v := ConvergenceVerdict{
		ChannelID:        "/sys/class/hwmon/hwmon3/pwm1",
		HwmonFingerprint: "deadbeef",
		D:                2,
		NCoupled:         0,
		NSamples:         50,
		NSamplesGate:     20,
		NSamplesPasses:   true,
		Theta:            []float64{0.9, -0.015},
		TrP:              10.0,
		TrPInitial:       100.0,
		TrPPasses:        true,
		Lambda:           0.99,
	}
	var buf bytes.Buffer
	emitVerdicts([]ConvergenceVerdict{v}, true, &buf)

	var got ConvergenceVerdict
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal NDJSON: %v\noutput: %s", err, buf.String())
	}
	if got.ChannelID != v.ChannelID {
		t.Errorf("channel: round-trip lost field")
	}
	if !got.NSamplesPasses {
		t.Errorf("n_samples_passes: round-trip lost field")
	}
}

// TestEmitVerdicts_Human contains expected substrings that operators
// grep for when scanning soak transcripts. A future format change
// must update this test deliberately rather than silently breaking
// downstream tooling.
func TestEmitVerdicts_Human(t *testing.T) {
	v := ConvergenceVerdict{
		ChannelID:      "/sys/class/hwmon/hwmon3/pwm1",
		D:              2,
		NSamples:       0,
		NSamplesGate:   20,
		NSamplesPasses: false,
		Theta:          []float64{0, 0},
		ThetaIsZero:    true,
	}
	var buf bytes.Buffer
	emitVerdicts([]ConvergenceVerdict{v}, false, &buf)
	out := buf.String()
	for _, want := range []string{
		"channel: /sys/class/hwmon/hwmon3/pwm1",
		"d / n_coupled:  2 / 0",
		"n_samples:      0 / 20 [--]",
		"warming up",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
}

// pSerialise marshals a row-major float64 matrix to little-endian
// bytes — mirrors what the upstream coupling.persistence does.
func pSerialise(mat []float64) []byte {
	raw := make([]byte, len(mat)*8)
	for i, v := range mat {
		bits := math.Float64bits(v)
		off := i * 8
		raw[off] = byte(bits)
		raw[off+1] = byte(bits >> 8)
		raw[off+2] = byte(bits >> 16)
		raw[off+3] = byte(bits >> 24)
		raw[off+4] = byte(bits >> 32)
		raw[off+5] = byte(bits >> 40)
		raw[off+6] = byte(bits >> 48)
		raw[off+7] = byte(bits >> 56)
	}
	return raw
}

// pSerialiseDiag builds the upper-triangle of a diagonal d×d matrix
// in coupling.serialiseSymDense layout — row-major with
// (0,0)(0,1)…(0,d-1)(1,1)(1,2)…(d-1,d-1). Diagonal entries take the
// supplied values; off-diagonals are zero. Matches the initial-P
// shape coupling.New produces (P_0 = InitialP × I).
func pSerialiseDiag(diag []float64) []byte {
	d := len(diag)
	upper := make([]float64, 0, d*(d+1)/2)
	for i := 0; i < d; i++ {
		for j := i; j < d; j++ {
			if i == j {
				upper = append(upper, diag[i])
			} else {
				upper = append(upper, 0)
			}
		}
	}
	return pSerialise(upper)
}
