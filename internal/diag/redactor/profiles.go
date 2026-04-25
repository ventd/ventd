package redactor

// Profile names for the three redaction tiers.
const (
	ProfileConservative     = "default-conservative"
	ProfileTrustedRecipient = "trusted-recipient"
	ProfileOff              = "off"
)

// Config drives a Redactor instance.
type Config struct {
	Profile             string   // ProfileConservative | ProfileTrustedRecipient | ProfileOff
	ExtraKeywords       []string // --redact-keyword values
	Hostname            string   // override for tests; empty → os.Hostname()
	Users               []string // override for tests; nil → auto-detect
	AllowRedactionFails bool     // --allow-redaction-failures
}

// DefaultConfig returns the default-conservative config.
func DefaultConfig() Config {
	return Config{Profile: ProfileConservative}
}

// buildPrimitives returns the ordered list of primitives for a given profile.
func buildPrimitives(cfg Config) []Primitive {
	users := cfg.Users
	if users == nil {
		users = collectHumanUsers()
	}
	var hostname string
	if cfg.Hostname != "" {
		hostname = cfg.Hostname
	}
	switch cfg.Profile {
	case ProfileOff:
		return nil
	case ProfileTrustedRecipient:
		// trusted-recipient keeps hostname (P1 disabled) but strips everything else.
		return []Primitive{
			&P2DMI{},
			&P3MAC{},
			&P4IP{},
			NewP5UsernameFrom(users),
			NewP6Path(users),
			&P7USBPhysical{},
			&P8Cmdline{},
			&P9UserLabel{},
			NewP10UserKeyword(cfg.ExtraKeywords),
		}
	default: // ProfileConservative
		var p1 *P1Hostname
		if hostname != "" {
			p1 = NewP1HostnameFrom(hostname)
		} else {
			p1 = NewP1Hostname()
		}
		return []Primitive{
			p1,
			&P2DMI{},
			&P3MAC{},
			&P4IP{},
			NewP5UsernameFrom(users),
			NewP6Path(users),
			&P7USBPhysical{},
			&P8Cmdline{},
			&P9UserLabel{},
			NewP10UserKeyword(cfg.ExtraKeywords),
		}
	}
}
