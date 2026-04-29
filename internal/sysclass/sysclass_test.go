package sysclass

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/state"
)

// writeFile creates parent dirs and writes content to path.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

// makeBase returns a deps with empty temp dirs and a no-op execDmidecode.
func makeBase(t *testing.T) (sysDir, procDir, devDir string, d deps) {
	t.Helper()
	sysDir = t.TempDir()
	procDir = t.TempDir()
	devDir = t.TempDir()
	d = deps{
		sysRoot:  sysDir,
		procRoot: procDir,
		devRoot:  devDir,
		execDmidecode: func(...string) (string, error) {
			return "", nil
		},
	}
	// Minimal cpuinfo so classifyCPU doesn't error.
	writeFile(t, filepath.Join(procDir, "cpuinfo"), "processor\t: 0\n")
	return
}

// setCPU writes a /proc/cpuinfo model name line.
func setCPU(t *testing.T, procDir, modelName string) {
	t.Helper()
	writeFile(t, filepath.Join(procDir, "cpuinfo"),
		"processor\t: 0\nmodel name\t: "+modelName+"\n")
}

// setRotationalDisk creates a block device entry with rotational=1.
func setRotationalDisk(t *testing.T, sysDir string) {
	t.Helper()
	writeFile(t, filepath.Join(sysDir, "block", "sda", "queue", "rotational"), "1\n")
}

// setMDRAID writes a minimal /proc/mdstat with one active array.
func setMDRAID(t *testing.T, procDir string) {
	t.Helper()
	writeFile(t, filepath.Join(procDir, "mdstat"),
		"Personalities : [raid1]\nmd0 : active raid1 sda1[0] sdb1[1]\n")
}

// setBattery creates a BAT0 entry so detectLaptop returns true.
func setBattery(t *testing.T, sysDir string) {
	t.Helper()
	writeFile(t, filepath.Join(sysDir, "class", "power_supply", "BAT0", "status"), "Discharging\n")
}

// setIPMI creates /dev/ipmi0 in devDir so detectBMC returns true.
func setIPMI(t *testing.T, devDir string) {
	t.Helper()
	writeFile(t, filepath.Join(devDir, "dev", "ipmi0"), "")
}

// emptyResult returns a ProbeResult with no channels or sensors.
func emptyResult() *probe.ProbeResult { return &probe.ProbeResult{} }

// withChannel adds one controllable channel to r and returns it.
func withChannel(r *probe.ProbeResult) *probe.ProbeResult {
	r.ControllableChannels = append(r.ControllableChannels, probe.ControllableChannel{
		PWMPath:  "/sys/class/hwmon/hwmon0/pwm1",
		Polarity: "unknown",
	})
	return r
}

// withSensor adds a ThermalSource with one sensor reading to r.
func withSensor(r *probe.ProbeResult, label string, reading float64) *probe.ProbeResult {
	r.ThermalSources = append(r.ThermalSources, probe.ThermalSource{
		Sensors: []probe.SensorChannel{
			{Path: "/sys/class/hwmon/hwmon0/temp1_input", Label: label, InitialRead: reading, ReadOK: true},
		},
	})
	return r
}

// openTestKV opens a fresh KV store in a temp dir.
func openTestKV(t *testing.T) *state.KVDB {
	t.Helper()
	st, err := state.Open(t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.KV
}

// TestRULE_SYSCLASS_01_PrecedenceOrder verifies that detectWithDeps evaluates
// classification rules in §3.2 order: NAS > MiniPC > Laptop > Server > HEDT > MidDesktop.
func TestRULE_SYSCLASS_01_PrecedenceOrder(t *testing.T) {
	ctx := context.Background()

	t.Run("nas_beats_laptop", func(t *testing.T) {
		sys, proc, dev, d := makeBase(t)
		// NAS conditions: rotational + mdraid
		setRotationalDisk(t, sys)
		setMDRAID(t, proc)
		// Laptop condition: battery present
		setBattery(t, sys)
		_ = dev
		det, err := detectWithDeps(ctx, emptyResult(), d)
		if err != nil {
			t.Fatal(err)
		}
		if det.Class != ClassNASHDD {
			t.Fatalf("expected ClassNASHDD, got %v", det.Class)
		}
	})

	t.Run("laptop_beats_server", func(t *testing.T) {
		sys, proc, dev, d := makeBase(t)
		// Laptop signal: battery
		setBattery(t, sys)
		// Server signal: EPYC CPU
		setCPU(t, proc, "AMD EPYC 7543 32-Core Processor")
		_ = dev
		det, err := detectWithDeps(ctx, emptyResult(), d)
		if err != nil {
			t.Fatal(err)
		}
		if det.Class != ClassLaptop {
			t.Fatalf("expected ClassLaptop (laptop beats server), got %v", det.Class)
		}
	})

	t.Run("mid_desktop_fallback_with_channels", func(t *testing.T) {
		_, proc, _, d := makeBase(t)
		setCPU(t, proc, "Intel Core i7-12700K")
		r := withChannel(emptyResult())
		det, err := detectWithDeps(ctx, r, d)
		if err != nil {
			t.Fatal(err)
		}
		if det.Class != ClassMidDesktop {
			t.Fatalf("expected ClassMidDesktop, got %v", det.Class)
		}
	})

	t.Run("hedt_air_beats_mid_desktop", func(t *testing.T) {
		_, proc, _, d := makeBase(t)
		// 13900K → HEDT, even with channels
		setCPU(t, proc, "Intel Core i9-13900K")
		r := withChannel(emptyResult())
		det, err := detectWithDeps(ctx, r, d)
		if err != nil {
			t.Fatal(err)
		}
		if det.Class != ClassHEDTAir {
			t.Fatalf("expected ClassHEDTAir, got %v", det.Class)
		}
	})
}

// TestRULE_SYSCLASS_02_KVWriteBeforeEnvelopeC verifies that PersistDetection
// writes the Detection to the `sysclass` KV namespace and LoadDetection
// reconstructs it faithfully with schema_version=1.
func TestRULE_SYSCLASS_02_KVWriteBeforeEnvelopeC(t *testing.T) {
	kv := openTestKV(t)

	// Start with no persisted detection.
	got, ok, err := LoadDetection(kv)
	if err != nil || ok || got != nil {
		t.Fatalf("expected absent detection; got ok=%v err=%v", ok, err)
	}

	ec := true
	det := &Detection{
		Class:         ClassMidDesktop,
		Evidence:      []string{"pwm_channels_present", "cpu_model_regex:intel_mid_12_13_gen"},
		Tjmax:         100.0,
		BMCPresent:    false,
		ECHandshakeOK: &ec,
		AmbientSensor: AmbientSensor{
			Source:      AmbientLabeled,
			SensorPath:  "/sys/class/hwmon/hwmon0/temp1_input",
			SensorLabel: "ambient",
			Reading:     22.5,
		},
	}

	if err := PersistDetection(kv, det); err != nil {
		t.Fatalf("PersistDetection: %v", err)
	}

	loaded, ok, err := LoadDetection(kv)
	if err != nil {
		t.Fatalf("LoadDetection: %v", err)
	}
	if !ok {
		t.Fatal("LoadDetection: expected ok=true")
	}
	if loaded.Class != det.Class {
		t.Errorf("Class: got %v, want %v", loaded.Class, det.Class)
	}
	if len(loaded.Evidence) != len(det.Evidence) {
		t.Errorf("Evidence len: got %d, want %d", len(loaded.Evidence), len(det.Evidence))
	}
	if loaded.Tjmax != det.Tjmax {
		t.Errorf("Tjmax: got %v, want %v", loaded.Tjmax, det.Tjmax)
	}
	if loaded.AmbientSensor.Source != det.AmbientSensor.Source {
		t.Errorf("Ambient.Source: got %v, want %v", loaded.AmbientSensor.Source, det.AmbientSensor.Source)
	}
	if loaded.AmbientSensor.Reading != det.AmbientSensor.Reading {
		t.Errorf("Ambient.Reading: got %v, want %v", loaded.AmbientSensor.Reading, det.AmbientSensor.Reading)
	}
}

// TestRULE_SYSCLASS_03_AmbientFallbackChain verifies the §3.3 three-step
// fallback: labeled → lowest-at-idle → 25 °C.
func TestRULE_SYSCLASS_03_AmbientFallbackChain(t *testing.T) {
	_, _, _, d := makeBase(t)

	t.Run("step1_labeled_sensor", func(t *testing.T) {
		r := emptyResult()
		withSensor(r, "Ambient Temp", 21.0)
		withSensor(r, "CPU Temp", 55.0)
		amb := identifyAmbient(r, d)
		if amb.Source != AmbientLabeled {
			t.Errorf("Source: got %v, want AmbientLabeled", amb.Source)
		}
		if amb.Reading != 21.0 {
			t.Errorf("Reading: got %v, want 21.0", amb.Reading)
		}
	})

	t.Run("step2_lowest_at_idle", func(t *testing.T) {
		r := emptyResult()
		// No labeled sensors; all admissible → pick lowest.
		withSensor(r, "System Temp", 35.0)
		withSensor(r, "Board Temp", 28.0)  // lowest admissible
		withSensor(r, "CPU Package", 62.0) // blocked by admissibility
		amb := identifyAmbient(r, d)
		if amb.Source != AmbientLowestAtIdle {
			t.Errorf("Source: got %v, want AmbientLowestAtIdle", amb.Source)
		}
		if amb.Reading != 28.0 {
			t.Errorf("Reading: got %v, want 28.0 (lowest admissible)", amb.Reading)
		}
	})

	t.Run("step3_fallback_25c", func(t *testing.T) {
		r := emptyResult()
		// Only blocked sensors — no admissible candidates.
		withSensor(r, "CPU Package", 55.0)
		withSensor(r, "CPU Core 0", 52.0)
		amb := identifyAmbient(r, d)
		if amb.Source != AmbientFallback25C {
			t.Errorf("Source: got %v, want AmbientFallback25C", amb.Source)
		}
		if amb.Reading != 25.0 {
			t.Errorf("Reading: got %v, want 25.0", amb.Reading)
		}
	})
}

// TestRULE_SYSCLASS_04_AmbientBoundsRefusal verifies that AmbientBoundsOK
// refuses readings below 10 °C and above 50 °C (RULE-SYSCLASS-04).
func TestRULE_SYSCLASS_04_AmbientBoundsRefusal(t *testing.T) {
	cases := []struct {
		reading  float64
		wantOK   bool
		wantCode string
	}{
		{9.9, false, "AMBIENT-IMPLAUSIBLE-TOO-COLD"},
		{10.0, true, ""},
		{25.0, true, ""},
		{50.0, true, ""},
		{50.1, false, "AMBIENT-IMPLAUSIBLE-TOO-HOT"},
		{-5.0, false, "AMBIENT-IMPLAUSIBLE-TOO-COLD"},
		{100.0, false, "AMBIENT-IMPLAUSIBLE-TOO-HOT"},
	}
	for _, c := range cases {
		code, ok := AmbientBoundsOK(c.reading)
		if ok != c.wantOK {
			t.Errorf("AmbientBoundsOK(%.1f): got ok=%v, want %v", c.reading, ok, c.wantOK)
		}
		if code != c.wantCode {
			t.Errorf("AmbientBoundsOK(%.1f): got code=%q, want %q", c.reading, code, c.wantCode)
		}
	}
}

// TestRULE_SYSCLASS_05_ServerBMCGate verifies that ServerProbeAllowed returns
// false only when cls==ClassServer AND bmcPresent AND !allowServerProbe.
func TestRULE_SYSCLASS_05_ServerBMCGate(t *testing.T) {
	cases := []struct {
		cls         SystemClass
		bmc         bool
		allow       bool
		wantAllowed bool
	}{
		{ClassServer, true, false, false},    // gated: BMC present, flag off
		{ClassServer, true, true, true},      // allowed: BMC present, flag on
		{ClassServer, false, false, true},    // allowed: no BMC, gate inactive
		{ClassMidDesktop, true, false, true}, // non-server: gate irrelevant
		{ClassHEDTAir, true, false, true},    // non-server
		{ClassLaptop, false, false, true},    // non-server
	}
	for _, c := range cases {
		got := ServerProbeAllowed(c.cls, c.bmc, c.allow)
		if got != c.wantAllowed {
			t.Errorf("ServerProbeAllowed(%v, bmc=%v, allow=%v) = %v, want %v",
				c.cls, c.bmc, c.allow, got, c.wantAllowed)
		}
	}
}

// TestRULE_SYSCLASS_06_LaptopECHandshake verifies ProbeECHandshake returns
// true when the RPM changes within the window, and false on context cancel.
func TestRULE_SYSCLASS_06_LaptopECHandshake(t *testing.T) {
	t.Run("success_rpm_changes", func(t *testing.T) {
		dir := t.TempDir()
		pwmPath := filepath.Join(dir, "pwm1_enable")
		rpmPath := filepath.Join(dir, "fan1_input")
		writeFile(t, pwmPath, "0\n")
		writeFile(t, rpmPath, "1000\n")

		// Goroutine writes new RPM after 80ms; poll loop checks every 200ms.
		done := make(chan struct{})
		go func() {
			defer close(done)
			time.Sleep(80 * time.Millisecond)
			_ = os.WriteFile(rpmPath, []byte("1500\n"), 0o644)
		}()

		ok, err := ProbeECHandshake(context.Background(), pwmPath, rpmPath)
		<-done
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok {
			t.Fatal("expected ok=true when RPM changes")
		}
	})

	t.Run("failure_context_cancelled", func(t *testing.T) {
		dir := t.TempDir()
		pwmPath := filepath.Join(dir, "pwm1_enable")
		rpmPath := filepath.Join(dir, "fan1_input")
		writeFile(t, pwmPath, "0\n")
		writeFile(t, rpmPath, "1000\n") // RPM never changes

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
		defer cancel()

		ok, err := ProbeECHandshake(ctx, pwmPath, rpmPath)
		if ok {
			t.Fatal("expected ok=false when context cancelled before RPM changes")
		}
		if err == nil {
			t.Fatal("expected non-nil error on context cancel")
		}
	})
}

// TestRULE_SYSCLASS_07_EvidenceCompleteness verifies that detectWithDeps
// populates a non-empty Evidence slice for every system class.
func TestRULE_SYSCLASS_07_EvidenceCompleteness(t *testing.T) {
	ctx := context.Background()

	t.Run("nas", func(t *testing.T) {
		sys, proc, _, d := makeBase(t)
		setRotationalDisk(t, sys)
		setMDRAID(t, proc)
		det, err := detectWithDeps(ctx, emptyResult(), d)
		if err != nil || det.Class != ClassNASHDD {
			t.Fatalf("expected NAS, got %v (err=%v)", det.Class, err)
		}
		if len(det.Evidence) == 0 {
			t.Error("NAS: Evidence is empty")
		}
	})

	t.Run("mini_pc", func(t *testing.T) {
		_, proc, _, d := makeBase(t)
		setCPU(t, proc, "Intel N100")
		det, err := detectWithDeps(ctx, emptyResult(), d)
		if err != nil || det.Class != ClassMiniPC {
			t.Fatalf("expected MiniPC, got %v (err=%v)", det.Class, err)
		}
		if len(det.Evidence) == 0 {
			t.Error("MiniPC: Evidence is empty")
		}
	})

	t.Run("laptop", func(t *testing.T) {
		sys, _, _, d := makeBase(t)
		setBattery(t, sys)
		det, err := detectWithDeps(ctx, emptyResult(), d)
		if err != nil || det.Class != ClassLaptop {
			t.Fatalf("expected Laptop, got %v (err=%v)", det.Class, err)
		}
		if len(det.Evidence) == 0 {
			t.Error("Laptop: Evidence is empty")
		}
	})

	t.Run("server", func(t *testing.T) {
		_, _, dev, d := makeBase(t)
		setIPMI(t, dev)
		det, err := detectWithDeps(ctx, emptyResult(), d)
		if err != nil || det.Class != ClassServer {
			t.Fatalf("expected Server, got %v (err=%v)", det.Class, err)
		}
		if len(det.Evidence) == 0 {
			t.Error("Server: Evidence is empty")
		}
	})

	t.Run("hedt_aio", func(t *testing.T) {
		_, proc, _, d := makeBase(t)
		setCPU(t, proc, "Intel Core i9-13900K")
		r := withChannel(emptyResult())
		withSensor(r, "coolant temp", 30.0)
		det, err := detectWithDeps(ctx, r, d)
		if err != nil || det.Class != ClassHEDTAIO {
			t.Fatalf("expected HEDT-AIO, got %v (err=%v)", det.Class, err)
		}
		if len(det.Evidence) == 0 {
			t.Error("HEDT-AIO: Evidence is empty")
		}
	})

	t.Run("hedt_air", func(t *testing.T) {
		_, proc, _, d := makeBase(t)
		setCPU(t, proc, "Intel Core i9-13900K")
		r := withChannel(emptyResult())
		det, err := detectWithDeps(ctx, r, d)
		if err != nil || det.Class != ClassHEDTAir {
			t.Fatalf("expected HEDT-Air, got %v (err=%v)", det.Class, err)
		}
		if len(det.Evidence) == 0 {
			t.Error("HEDT-Air: Evidence is empty")
		}
	})

	t.Run("mid_desktop", func(t *testing.T) {
		_, proc, _, d := makeBase(t)
		setCPU(t, proc, "Intel Core i7-12700K")
		r := withChannel(emptyResult())
		det, err := detectWithDeps(ctx, r, d)
		if err != nil || det.Class != ClassMidDesktop {
			t.Fatalf("expected MidDesktop, got %v (err=%v)", det.Class, err)
		}
		if len(det.Evidence) == 0 {
			t.Error("MidDesktop: Evidence is empty")
		}
	})
}
