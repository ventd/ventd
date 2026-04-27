package experimental_test

import (
	"testing"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/experimental"
)

func TestParseConfig_CopiesFields(t *testing.T) {
	cfg := config.ExperimentalConfig{
		AMDOverdrive:    true,
		NVIDIACoolbits:  false,
		ILO4Unlocked:    true,
		IDRAC9LegacyRaw: false,
	}
	got := experimental.ParseConfig(cfg)
	if !got.AMDOverdrive {
		t.Error("AMDOverdrive should be true")
	}
	if got.NVIDIACoolbits {
		t.Error("NVIDIACoolbits should be false")
	}
	if !got.ILO4Unlocked {
		t.Error("ILO4Unlocked should be true")
	}
	if got.IDRAC9LegacyRaw {
		t.Error("IDRAC9LegacyRaw should be false")
	}
}

// TestMerge_PrecedenceCLIOverConfig binds RULE-EXPERIMENTAL-FLAG-PRECEDENCE:
// CLI flags override config-file values; neither overrides a false with false.
func TestMerge_PrecedenceCLIOverConfig(t *testing.T) {
	tests := []struct {
		name string
		cli  experimental.Flags
		cfg  experimental.Flags
		want experimental.Flags
	}{
		{
			name: "CLI true wins over config false",
			cli:  experimental.Flags{AMDOverdrive: true},
			cfg:  experimental.Flags{AMDOverdrive: false},
			want: experimental.Flags{AMDOverdrive: true},
		},
		{
			name: "config true propagates when CLI false",
			cli:  experimental.Flags{AMDOverdrive: false},
			cfg:  experimental.Flags{AMDOverdrive: true},
			want: experimental.Flags{AMDOverdrive: true},
		},
		{
			name: "both true yields true",
			cli:  experimental.Flags{ILO4Unlocked: true},
			cfg:  experimental.Flags{ILO4Unlocked: true},
			want: experimental.Flags{ILO4Unlocked: true},
		},
		{
			name: "both false yields false",
			cli:  experimental.Flags{},
			cfg:  experimental.Flags{},
			want: experimental.Flags{},
		},
		{
			name: "multiple flags merged independently",
			cli:  experimental.Flags{AMDOverdrive: true, NVIDIACoolbits: false},
			cfg:  experimental.Flags{AMDOverdrive: false, NVIDIACoolbits: true, ILO4Unlocked: true},
			want: experimental.Flags{AMDOverdrive: true, NVIDIACoolbits: true, ILO4Unlocked: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := experimental.Merge(tc.cli, tc.cfg)
			if got != tc.want {
				t.Errorf("Merge(%+v, %+v) = %+v, want %+v", tc.cli, tc.cfg, got, tc.want)
			}
		})
	}
}
