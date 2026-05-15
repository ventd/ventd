package nbfc

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/ventd/ventd/internal/acpi"
	"github.com/ventd/ventd/internal/ec"
	"github.com/ventd/ventd/internal/hal"
	nbfcdb "github.com/ventd/ventd/internal/hwdb/nbfc"
)

// Backend is the HAL backend for laptop ECs driven via the upstream
// nbfc-linux catalogue. Each FanConfiguration in the matched config
// becomes one hal.Channel; reads / writes route through the
// register-allowlisted internal/ec.Transport.
//
// Writes are unconditional: the upstream catalogue's per-model
// register map is the trust boundary, the closed-set allowlist on
// the EC transport (RULE-NBFC-EC-02) is the runtime gate, and
// RULE-IDLE-02 (battery) + RULE-IDLE-03 (container) refuse the
// daemon entirely on hosts where writes are unsafe. No artificial
// --enable-nbfc-write gate; see feedback-dont-default-writes-off.
type Backend struct {
	cfg         *nbfcdb.Config
	cfgFilename string
	transport   ec.Transport
	acpi        *acpi.Bridge // nil when config uses no ACPI methods

	mu       sync.Mutex
	channels []hal.Channel
}

// ProbeOpts carries the construction inputs. Tests inject their own
// Catalog + Transport via the same struct.
type ProbeOpts struct {
	// Config is the matched upstream catalogue entry. Required.
	Config *nbfcdb.Config

	// Filename is the upstream config path (for diagnostics / Doctor
	// surface). Optional.
	Filename string

	// Transport is the EC transport. The Backend wraps it in a
	// register allowlist derived from Config.RegistersUsed() so the
	// caller can pass either a raw ec.Transport from ec.Available()
	// or a pre-wrapped one (tests). Required when the config uses
	// any register (the common case); may be nil for pure-ACPI configs
	// once ACPI is wired (PR B3 — not yet shipped).
	Transport ec.Transport

	// ACPI is the bridge for configs that invoke ACPI methods. When
	// the matched config uses no ACPI methods, set this to nil. When
	// the config uses ACPI and ACPI is nil, New refuses with
	// ErrNBFCConfigNeedsAcpiBridge. The bridge's allowlist should
	// come from Config.AcpiMethodsUsed().
	ACPI *acpi.Bridge
}

// New constructs a Backend over the given config + transport. Returns
// an error when the config requires Lua (no runtime in v0.8.0) or
// uses ACPI methods (PR B3 — the caller should have routed those
// through the ACPI-aware constructor).
func New(opts ProbeOpts) (*Backend, error) {
	if opts.Config == nil {
		return nil, fmt.Errorf("nbfc: New: Config is nil")
	}
	if opts.Config.UsesLua() {
		return nil, ErrNBFCConfigNeedsLuaRuntime
	}
	// ACPI configs need the bridge; register configs need the transport.
	// A config can use both (Mixed). We require both surfaces to be
	// present when each is actually invoked.
	usesACPI := opts.Config.UsesACPI()
	if usesACPI && opts.ACPI == nil {
		return nil, ErrNBFCConfigNeedsAcpiBridge
	}
	usesRegisters := len(opts.Config.RegistersUsed()) > 0
	if usesRegisters && opts.Transport == nil {
		return nil, fmt.Errorf("%w: register-touching config requires EC transport", ErrNBFCNoTransport)
	}

	var wrapped ec.Transport
	if usesRegisters {
		wrapped = ec.WithAllowlist(opts.Transport, opts.Config.RegistersUsed())
	}

	b := &Backend{
		cfg:         opts.Config,
		cfgFilename: opts.Filename,
		transport:   wrapped,
		acpi:        opts.ACPI,
	}
	b.buildChannels()
	return b, nil
}

// buildChannels populates the hal.Channel slice once at construction.
// Channel ID is derived from FanDisplayName so it stays stable across
// reboots (the upstream config doesn't change between syncs).
func (b *Backend) buildChannels() {
	chans := make([]hal.Channel, 0, len(b.cfg.FanConfigurations))
	for i := range b.cfg.FanConfigurations {
		fan := &b.cfg.FanConfigurations[i]
		caps := hal.CapRead | hal.CapWritePWM | hal.CapRestore
		chans = append(chans, hal.Channel{
			ID:     channelID(fan, i),
			Role:   inferRole(fan.FanDisplayName),
			Caps:   caps,
			Opaque: fan, // *FanConfiguration; used by Read / Write / Restore
		})
	}
	b.channels = chans
}

func channelID(fan *nbfcdb.FanConfiguration, idx int) string {
	name := strings.TrimSpace(fan.FanDisplayName)
	if name == "" {
		return fmt.Sprintf("fan%d", idx)
	}
	// Sanitise: ASCII letters / digits / underscore; spaces → underscore.
	var out strings.Builder
	for _, r := range name {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			out.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			out.WriteByte('_')
		}
	}
	if out.Len() == 0 {
		return fmt.Sprintf("fan%d", idx)
	}
	return out.String()
}

func inferRole(displayName string) hal.ChannelRole {
	lower := strings.ToLower(displayName)
	switch {
	case strings.Contains(lower, "cpu"):
		return hal.RoleCPU
	case strings.Contains(lower, "gpu") || strings.Contains(lower, "vga"):
		return hal.RoleGPU
	case strings.Contains(lower, "pump"):
		return hal.RolePump
	case strings.Contains(lower, "case") || strings.Contains(lower, "chassis"):
		return hal.RoleCase
	}
	return hal.RoleUnknown
}

// Name returns the stable backend identifier used in
// `hal.Register("nbfc", ...)`.
func (b *Backend) Name() string { return "nbfc" }

// Enumerate returns the channel slice constructed at New time. The
// nbfc catalogue is static across the backend's lifetime; hot-plug
// isn't a thing for built-in laptop fans.
func (b *Backend) Enumerate(ctx context.Context) ([]hal.Channel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Return a copy to keep callers from mutating the backend's slice.
	out := make([]hal.Channel, len(b.channels))
	copy(out, b.channels)
	return out, nil
}

// Read samples the channel's current PWM. RPM isn't surfaced by the
// nbfc schema (no fan-tach register declared in upstream configs),
// so Reading.RPM stays 0 with OK=true when the PWM read succeeds.
func (b *Backend) Read(ch hal.Channel) (hal.Reading, error) {
	fan, ok := ch.Opaque.(*nbfcdb.FanConfiguration)
	if !ok || fan == nil {
		return hal.Reading{}, fmt.Errorf("nbfc: Read: channel %q has no FanConfiguration", ch.ID)
	}
	val, err := b.readFanRegister(fan)
	if err != nil {
		return hal.Reading{OK: false}, err
	}
	return hal.Reading{
		PWM:  registerToPWM(val, *fan),
		RPM:  0,
		Temp: 0,
		OK:   true,
	}, nil
}

func (b *Backend) readFanRegister(fan *nbfcdb.FanConfiguration) (uint16, error) {
	if strings.TrimSpace(fan.ReadAcpiMethod) != "" {
		if b.acpi == nil {
			return 0, ErrNBFCConfigNeedsAcpiBridge
		}
		v, err := b.acpi.Call(fan.ReadAcpiMethod)
		return uint16(v), err
	}
	if b.cfg.ReadWriteWords {
		return b.transport.Read16(fan.ReadRegister)
	}
	v, err := b.transport.Read(fan.ReadRegister)
	if err != nil {
		return 0, err
	}
	return uint16(v), nil
}

// Write commands the channel to a 0-255 PWM byte. Scaled into the
// upstream register range via pwmToRegister, then dispatched through
// the closed-set register allowlist (or ACPI bridge when the matched
// fan declares a WriteAcpiMethod).
func (b *Backend) Write(ch hal.Channel, pwm uint8) error {
	fan, ok := ch.Opaque.(*nbfcdb.FanConfiguration)
	if !ok || fan == nil {
		return fmt.Errorf("nbfc: Write: channel %q has no FanConfiguration", ch.ID)
	}
	val := pwmToRegister(pwm, *fan)
	return b.writeFanRegister(fan, val)
}

func (b *Backend) writeFanRegister(fan *nbfcdb.FanConfiguration, val uint16) error {
	if strings.TrimSpace(fan.WriteAcpiMethod) != "" {
		if b.acpi == nil {
			return ErrNBFCConfigNeedsAcpiBridge
		}
		_, err := b.acpi.Call(fan.WriteAcpiMethod, uint64(val))
		return err
	}
	if b.cfg.ReadWriteWords {
		return b.transport.Write16(fan.WriteRegister, val)
	}
	return b.transport.Write(fan.WriteRegister, uint8(val))
}

// Restore returns the channel to firmware-managed mode by writing
// the upstream-declared `FanSpeedResetValue` to the WriteRegister
// (when ResetRequired) and then applying every RegisterWriteConfig
// with ResetRequired=true. Idempotent + safe to call on a channel
// that was never Written.
func (b *Backend) Restore(ch hal.Channel) error {
	fan, ok := ch.Opaque.(*nbfcdb.FanConfiguration)
	if !ok || fan == nil {
		return nil
	}
	if fan.ResetRequired {
		// ResetAcpiMethod takes precedence when set; otherwise we use
		// the standard WriteRegister path with FanSpeedResetValue.
		if m := strings.TrimSpace(fan.ResetAcpiMethod); m != "" {
			if b.acpi == nil {
				return ErrNBFCConfigNeedsAcpiBridge
			}
			if _, err := b.acpi.Call(m, uint64(fan.FanSpeedResetValue)); err != nil {
				return fmt.Errorf("nbfc: Restore fan %q via ACPI: %w", ch.ID, err)
			}
		} else if err := b.writeFanRegister(fan, fan.FanSpeedResetValue); err != nil {
			return fmt.Errorf("nbfc: Restore fan %q: %w", ch.ID, err)
		}
	}
	// Apply every RegisterWriteConfig with ResetRequired=true.
	for _, rw := range b.cfg.RegisterWriteConfigurations {
		if !rw.ResetRequired {
			continue
		}
		if err := b.applyRegisterReset(rw); err != nil {
			return fmt.Errorf("nbfc: Restore RegisterWriteConfig reg=%#x: %w", rw.Register, err)
		}
	}
	return nil
}

// applyRegisterReset writes the reset value with the declared mode.
// Set = direct; And / Or = read-modify-write. Call / Lua are refused
// (the v0.8.0 register-only backend doesn't dispatch those; PR B3's
// ACPI-aware backend will).
func (b *Backend) applyRegisterReset(rw nbfcdb.RegisterWriteConfiguration) error {
	mode := rw.ResetWriteMode
	if mode == "" {
		mode = rw.WriteMode
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "set", "":
		return b.transport.Write(rw.Register, rw.ResetValue)
	case "and":
		cur, err := b.transport.Read(rw.Register)
		if err != nil {
			return err
		}
		return b.transport.Write(rw.Register, cur&rw.ResetValue)
	case "or":
		cur, err := b.transport.Read(rw.Register)
		if err != nil {
			return err
		}
		return b.transport.Write(rw.Register, cur|rw.ResetValue)
	case "call":
		// ACPI method dispatch via the bridge — refuse cleanly when
		// the bridge is unwired (PR B2 register-only path).
		if b.acpi == nil {
			return ErrNBFCConfigNeedsAcpiBridge
		}
		m := strings.TrimSpace(rw.ResetAcpiMethod)
		if m == "" {
			m = strings.TrimSpace(rw.AcpiMethod)
		}
		if m == "" {
			return fmt.Errorf("nbfc: WriteMode=Call with no method path for reg %#x", rw.Register)
		}
		_, err := b.acpi.Call(m, uint64(rw.ResetValue))
		return err
	case "lua":
		return ErrNBFCConfigNeedsLuaRuntime
	}
	return fmt.Errorf("nbfc: unknown WriteMode %q on reset for reg %#x", mode, rw.Register)
}

// Close releases the underlying EC transport. The ACPI bridge has
// no per-call file handle to release (each Call opens + closes
// /proc/acpi/call). Idempotent.
func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.transport == nil {
		return nil
	}
	err := b.transport.Close()
	b.transport = nil
	return err
}

// Config returns the matched upstream config, useful for the doctor
// detector + diagnostic bundles. Read-only.
func (b *Backend) Config() *nbfcdb.Config { return b.cfg }

// Filename returns the upstream source filename for the matched
// config (empty when not set at construction).
func (b *Backend) Filename() string { return b.cfgFilename }
