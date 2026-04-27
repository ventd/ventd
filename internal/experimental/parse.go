package experimental

import "github.com/ventd/ventd/internal/config"

// ParseConfig converts a config-file ExperimentalConfig into a Flags value.
func ParseConfig(cfg config.ExperimentalConfig) Flags {
	return Flags{
		AMDOverdrive:    cfg.AMDOverdrive,
		NVIDIACoolbits:  cfg.NVIDIACoolbits,
		ILO4Unlocked:    cfg.ILO4Unlocked,
		IDRAC9LegacyRaw: cfg.IDRAC9LegacyRaw,
	}
}

// Merge combines CLI flags and config-file flags: a flag is active if either
// source enables it. This satisfies CLI > config > default precedence for
// additive boolean flags — stdlib flag cannot distinguish an explicit false
// from an unset flag, so OR-merge is the correct rule.
func Merge(cli, cfg Flags) Flags {
	return Flags{
		AMDOverdrive:    cli.AMDOverdrive || cfg.AMDOverdrive,
		NVIDIACoolbits:  cli.NVIDIACoolbits || cfg.NVIDIACoolbits,
		ILO4Unlocked:    cli.ILO4Unlocked || cfg.ILO4Unlocked,
		IDRAC9LegacyRaw: cli.IDRAC9LegacyRaw || cfg.IDRAC9LegacyRaw,
	}
}
