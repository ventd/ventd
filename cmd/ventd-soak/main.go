// Command ventd-soak — Phase C smart-mode HIL field-validation harness.
//
// Read-only observer + (deferred, opt-in) synthetic excitation driver
// for smart-mode HIL soak runs. Reads Layer-B coupling shards from
// $VENTD_STATE_DIR/smart/shard-B/*.cbor directly via msgpack, decodes
// the on-disk Bucket shape (channel ID, theta, P, kappa, n_samples,
// hwmon fingerprint), and prints convergence metrics per channel.
//
// Subcommands:
//
//	ventd-soak snapshot          one-shot Layer-B state per channel + convergence verdict
//	ventd-soak watch [--interval=60s]   poll snapshot every N seconds
//	ventd-soak excite ...        DRIVE synthetic Δpwm steps (DEFERRED, opt-in only)
//
// Flags:
//
//	--state-dir <path>           default /var/lib/ventd
//	--json                       emit NDJSON (one channel per line) instead of human-readable
//	--enable-soak-excitation     required for `excite` subcommand (RULE-SOAK-EXCITATION-OPT-IN)
//
// Read-only by default: snapshot + watch don't write to any sysfs path,
// don't open any HID device, don't talk to the daemon. Pure file-system
// observer of the daemon's persisted shard state. Safe to run alongside
// the production daemon — the daemon owns Save; this tool only Reads.
//
// Per the v0.6.0 ship plan at /root/.claude/plans/you-are-a-30-vivid-pascal.md
// Phase C item C1.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

// SoakBucket mirrors internal/coupling.Bucket on disk. Read-only access
// to Layer-B shards via direct msgpack-decode of the .cbor files. We
// don't import internal/coupling to keep this binary's dep graph tight
// (no gonum, no synthetic-RLS deps) — the on-disk schema is stable
// (RULE-CPL-PERSIST-02 pins schema_version=1) and the field tags
// match the upstream struct.
type SoakBucket struct {
	SchemaVersion    uint8     `msgpack:"v"`
	HwmonFingerprint string    `msgpack:"hwfp"`
	ChannelID        string    `msgpack:"ch"`
	NCoupled         int       `msgpack:"nc"`
	Theta            []float64 `msgpack:"theta"`
	PSerialised      []byte    `msgpack:"p"`
	Lambda           float64   `msgpack:"lambda"`
	NSamples         uint64    `msgpack:"n"`
	LastSeenUnix     int64     `msgpack:"last"`
	GroupedFans      []int     `msgpack:"groups,omitempty"`
}

// ConvergenceVerdict is the per-channel state ventd-soak prints. Maps
// the on-disk Bucket fields to the warmup gate from RULE-CPL-WARMUP-01:
// n_samples ≥ 5·d² AND tr(P) ≤ 0.5·tr(P_0) AND κ ≤ 1e4. Without the
// runtime κ value (the on-disk Bucket doesn't persist it; it's
// recomputed on each tick from the windowed regressor), the verdict
// is best-effort: we report n_samples / TrP / theta non-zero / age and
// let the operator infer.
type ConvergenceVerdict struct {
	ChannelID        string    `json:"channel_id"`
	HwmonFingerprint string    `json:"hwmon_fingerprint"`
	D                int       `json:"d"`         // dimension = 1 + NCoupled + 1
	NCoupled         int       `json:"n_coupled"` // cross-channel cross-couplings
	NSamples         uint64    `json:"n_samples"`
	NSamplesGate     uint64    `json:"n_samples_gate"` // 5 · d²
	NSamplesPasses   bool      `json:"n_samples_passes"`
	Theta            []float64 `json:"theta"`
	ThetaIsZero      bool      `json:"theta_is_zero"`
	TrP              float64   `json:"tr_p"`
	TrPInitial       float64   `json:"tr_p_initial"` // d × InitialP — assume InitialP=50 per v0.5.7 default
	TrPPasses        bool      `json:"tr_p_passes"`  // tr(P) ≤ 0.5 · tr(P_0)
	Lambda           float64   `json:"lambda"`
	LastSeen         time.Time `json:"last_seen"`
	AgeSeconds       float64   `json:"age_seconds"`
	GroupedFans      []int     `json:"grouped_fans,omitempty"`
}

// initialPDefault is the v0.5.7+ default initial covariance scaling
// per coupling.Config. The on-disk Bucket doesn't persist InitialP
// directly so we assume the default; an operator who tunes it can
// override via --initial-p (deferred to v0.7+).
const initialPDefault = 50.0

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	switch os.Args[1] {
	case "snapshot":
		os.Exit(runSnapshot(os.Args[2:], logger))
	case "watch":
		os.Exit(runWatch(os.Args[2:], logger))
	case "excite":
		fmt.Fprintln(os.Stderr, "ventd-soak excite: DEFERRED — synthetic excitation driver requires daemon HTTP API integration; landing in a v0.7+ follow-up. RULE-SOAK-EXCITATION-OPT-IN scaffolds the gate.")
		os.Exit(2)
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	case "version", "-v", "--version":
		fmt.Println("ventd-soak — Phase C smart-mode HIL harness (v0.5.x)")
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "ventd-soak: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `ventd-soak — Phase C smart-mode HIL field-validation harness

Usage:
  ventd-soak snapshot [--state-dir DIR] [--json]
      One-shot Layer-B state per channel + convergence verdict.

  ventd-soak watch [--state-dir DIR] [--json] [--interval=60s]
      Poll snapshot every interval and emit deltas.

  ventd-soak excite --enable-soak-excitation ...   (DEFERRED — see RULE-SOAK-EXCITATION-OPT-IN)
      DRIVE synthetic Δpwm steps to provide RLS excitation. Opt-in.

  ventd-soak version
      Print version and exit.

Default --state-dir: /var/lib/ventd

The snapshot / watch subcommands read $STATE_DIR/smart/shard-B/*.cbor
directly via msgpack-decode. Pure read-only — safe to run alongside
the production daemon.
`)
}

func runSnapshot(args []string, logger *slog.Logger) int {
	fs := flag.NewFlagSet("snapshot", flag.ExitOnError)
	stateDir := fs.String("state-dir", "/var/lib/ventd", "ventd state directory")
	asJSON := fs.Bool("json", false, "emit NDJSON instead of human-readable")
	_ = fs.Parse(args)

	verdicts, err := readShards(*stateDir)
	if err != nil {
		logger.Error("snapshot: read shards", "err", err)
		return 1
	}
	if len(verdicts) == 0 {
		fmt.Fprintf(os.Stderr, "no Layer-B shards found in %s/smart/shard-B/ — has the daemon run on this host yet?\n", *stateDir)
		return 0
	}
	emitVerdicts(verdicts, *asJSON, os.Stdout)
	return 0
}

func runWatch(args []string, logger *slog.Logger) int {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	stateDir := fs.String("state-dir", "/var/lib/ventd", "ventd state directory")
	asJSON := fs.Bool("json", false, "emit NDJSON instead of human-readable")
	interval := fs.Duration("interval", 60*time.Second, "poll interval")
	_ = fs.Parse(args)

	if *interval < time.Second {
		fmt.Fprintln(os.Stderr, "watch: --interval must be at least 1s")
		return 2
	}
	fmt.Fprintf(os.Stderr, "ventd-soak watch: polling %s/smart/shard-B every %v (Ctrl-C to stop)\n", *stateDir, *interval)

	for {
		verdicts, err := readShards(*stateDir)
		if err != nil {
			logger.Error("watch: read shards", "err", err)
			// Keep polling — the daemon may be mid-restart.
		} else {
			wprintf(os.Stdout, "\n=== %s ===\n", time.Now().UTC().Format(time.RFC3339))
			emitVerdicts(verdicts, *asJSON, os.Stdout)
		}
		time.Sleep(*interval)
	}
}

// readShards walks $stateDir/smart/shard-B/*.cbor, msgpack-decodes
// each file into SoakBucket, and returns one ConvergenceVerdict per
// shard. Files that fail to decode are skipped with a stderr warning;
// missing directory returns an empty slice + nil error.
func readShards(stateDir string) ([]ConvergenceVerdict, error) {
	dir := filepath.Join(stateDir, "smart", "shard-B")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var verdicts []ConvergenceVerdict
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cbor") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "warn: read %s: %v\n", path, readErr)
			continue
		}
		var b SoakBucket
		if err := msgpack.Unmarshal(data, &b); err != nil {
			fmt.Fprintf(os.Stderr, "warn: decode %s: %v\n", path, err)
			continue
		}
		verdicts = append(verdicts, deriveVerdict(b))
	}
	return verdicts, nil
}

// deriveVerdict converts a decoded SoakBucket into the on-screen
// convergence verdict. Computes:
//
//	d            = 1 + NCoupled + 1                 (RULE-CPL-SHARD-01)
//	n_samples_gate = 5 · d²                          (RULE-CPL-WARMUP-01)
//	theta_is_zero = all entries in Theta are 0       (warmup hasn't accumulated)
//	tr_p         = trace of the d×d covariance matrix from PSerialised
//	tr_p_initial = d · InitialP_default              (assume v0.5.7+ default = 50)
//	tr_p_passes   = tr_p ≤ 0.5 · tr_p_initial         (RULE-CPL-WARMUP-01)
//
// PSerialised is "little-endian float64 raw, length d² × 8" per the
// upstream comment. We extract the diagonal entries by indexing
// (i, i) → byte offset i·d·8 + i·8.
func deriveVerdict(b SoakBucket) ConvergenceVerdict {
	d := 1 + b.NCoupled + 1
	nGate := uint64(5 * d * d)
	thetaZero := true
	for _, v := range b.Theta {
		if v != 0 {
			thetaZero = false
			break
		}
	}
	trP := traceFromSerialised(b.PSerialised, d)
	trPInit := float64(d) * initialPDefault
	last := time.Unix(b.LastSeenUnix, 0).UTC()
	age := 0.0
	if b.LastSeenUnix > 0 {
		age = time.Since(last).Seconds()
	}
	return ConvergenceVerdict{
		ChannelID:        b.ChannelID,
		HwmonFingerprint: b.HwmonFingerprint,
		D:                d,
		NCoupled:         b.NCoupled,
		NSamples:         b.NSamples,
		NSamplesGate:     nGate,
		NSamplesPasses:   b.NSamples >= nGate,
		Theta:            b.Theta,
		ThetaIsZero:      thetaZero,
		TrP:              trP,
		TrPInitial:       trPInit,
		TrPPasses:        trP > 0 && trP <= 0.5*trPInit,
		Lambda:           b.Lambda,
		LastSeen:         last,
		AgeSeconds:       age,
		GroupedFans:      b.GroupedFans,
	}
}

// traceFromSerialised extracts tr(P) from the upper-triangle raw blob
// produced by coupling.serialiseSymDense. The upstream layout is the
// upper triangle of a symmetric d×d matrix, row-major little-endian
// float64 — length = d·(d+1)/2 × 8, NOT d²×8.
//
// Diagonal entry (i,i) sits at flat index i·d − i·(i−1)/2 in the upper
// triangle (sum of row-widths above row i). The trace is the sum of
// those d entries. Returns 0 on malformed input — operator sees
// `tr_p_passes=false` and investigates rather than crash.
func traceFromSerialised(raw []byte, d int) float64 {
	expectedLen := d * (d + 1) / 2 * 8
	if len(raw) < expectedLen {
		return 0
	}
	trace := 0.0
	for i := 0; i < d; i++ {
		idx := i*d - i*(i-1)/2
		off := idx * 8
		// Decode little-endian float64 from raw[off:off+8] without
		// pulling in encoding/binary just for one path.
		bits := uint64(raw[off]) |
			uint64(raw[off+1])<<8 |
			uint64(raw[off+2])<<16 |
			uint64(raw[off+3])<<24 |
			uint64(raw[off+4])<<32 |
			uint64(raw[off+5])<<40 |
			uint64(raw[off+6])<<48 |
			uint64(raw[off+7])<<56
		trace += math.Float64frombits(bits)
	}
	return trace
}
