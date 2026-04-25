package hwdb

import (
	"testing"
	"time"
)

// TestRuleHwdbPR2_10 verifies RULE-HWDB-PR2-10: layer precedence
// (board > chip > driver, calibration > all for runtime fields) is enforced.
//
// Synthetic fixture:
//   - driver = nct6775 (catalog defaults: OffBehaviour=stops, PollingLatency=50ms)
//   - chip = nct6798 (overrides OffBehaviour → bios_dependent)
//   - board = asus_z790_a (overrides PollingLatencyHint → 75ms, CPUTINFloats=true)
//   - calibration channel 1 = PWMPolarity=inverted
func TestRuleHwdbPR2_10(t *testing.T) {
	t.Run("TestRuleHwdbPR2_10", func(t *testing.T) {
		cat := mustLoadEmbeddedCatalog(t)

		driver, ok := cat.Drivers["nct6775"]
		if !ok {
			t.Fatal("nct6775 driver not in embedded catalog")
		}
		chip, ok := cat.Chips["nct6798"]
		if !ok {
			t.Fatal("nct6798 chip not in embedded catalog")
		}

		// Chip override: OffBehaviour → bios_dependent (beats driver's "stops").
		offBehaviourOverride := OffBehaviourBIOSDependent
		chip.Overrides.OffBehaviour = &offBehaviourOverride

		// Board: overrides PollingLatencyHint → 75ms and sets CPUTINFloats=true.
		latency75 := 75
		board := &BoardProfileV2{
			ID:             "asus_z790_a",
			DMIBoardVendor: "ASUSTeK COMPUTER INC.",
			DMIBoardName:   "ROG STRIX Z790-A GAMING WIFI",
			Overrides: BoardOverrides{
				PollingLatencyMSHint: &latency75,
				CPUTINFloats:         true,
			},
		}

		// Calibration: channel 1 has PolarityInverted=true.
		cal := map[ChannelKey]*ChannelCalibration{
			{Hwmon: "nct6798", Index: 1}: {
				HwmonName:        "nct6798",
				ChannelIndex:     1,
				PolarityInverted: true,
				BIOSOverridden:   false,
			},
		}

		ecp := ResolveEffectiveProfile(driver, chip, board, cal, MatchDiagnostics{})

		// Layer 1 → layer 2: chip beats driver on OffBehaviour.
		if ecp.OffBehaviour != OffBehaviourBIOSDependent {
			t.Errorf("chip beats driver: want OffBehaviour=%q, got %q",
				OffBehaviourBIOSDependent, ecp.OffBehaviour)
		}

		// Layer 2 → layer 3: board beats chip on PollingLatencyHint.
		want75 := 75 * time.Millisecond
		if ecp.PollingLatencyHint != want75 {
			t.Errorf("board beats chip: want PollingLatencyHint=%v, got %v",
				want75, ecp.PollingLatencyHint)
		}

		// Layer 3: board sets CPUTINFloats=true.
		if !ecp.CPUTINFloats {
			t.Error("board beats chip: want CPUTINFloats=true")
		}

		// Layer 3: board ID is recorded.
		if ecp.BoardID == nil || *ecp.BoardID != "asus_z790_a" {
			t.Errorf("board ID: want %q, got %v", "asus_z790_a", ecp.BoardID)
		}

		// Layer 4: calibration is accessible by channel key.
		calCh1, hasCal := ecp.CalibrationByChannel[ChannelKey{Hwmon: "nct6798", Index: 1}]
		if !hasCal {
			t.Fatal("layer-4 calibration: channel 1 should be present")
		}
		if !calCh1.PolarityInverted {
			t.Errorf("layer-4 calibration: want PolarityInverted=true, got false")
		}

		// Driver base values not overridden must pass through.
		if ecp.Module != "nct6775" {
			t.Errorf("driver passthrough: want Module=nct6775, got %q", ecp.Module)
		}
		if ecp.ChipName != "nct6798" {
			t.Errorf("chip name recorded: want ChipName=nct6798, got %q", ecp.ChipName)
		}
		if !ecp.FirmwareCurveOffloadCapable {
			t.Error("nct6775 has smart_fan_iv → FirmwareCurveOffloadCapable should be true")
		}
	})
}
