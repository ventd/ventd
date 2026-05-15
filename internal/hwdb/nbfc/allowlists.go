package nbfc

import "strings"

// RegistersUsed returns the set of 8-bit EC register addresses this
// config touches across every Read / Write / Reset operation on
// every fan + every RegisterWriteConfiguration. 16-bit operations
// (ReadWriteWords) include both `reg` and `reg+1`. The set feeds
// `internal/ec.WithAllowlist` (RULE-NBFC-EC-02) so the EC transport
// refuses any register access the catalogue hasn't declared safe.
//
// A nil receiver returns nil — every transport gate is effectively
// closed; useful when no config matched and we want absolute safety.
func (c *Config) RegistersUsed() map[uint8]bool {
	if c == nil {
		return nil
	}
	out := make(map[uint8]bool)
	add := func(reg uint8) {
		out[reg] = true
		if c.ReadWriteWords {
			out[reg+1] = true
		}
	}
	for _, fan := range c.FanConfigurations {
		// Read path: ACPI method or Lua takes precedence; otherwise
		// the register is used. Skip a zero register when a method is
		// declared — that's the upstream convention for "no register".
		readByMethod := strings.TrimSpace(fan.ReadAcpiMethod) != "" || !fan.ReadLuaCode.IsEmpty()
		if !readByMethod && fan.ReadRegister != 0 {
			add(fan.ReadRegister)
		}
		writeByMethod := strings.TrimSpace(fan.WriteAcpiMethod) != "" || !fan.WriteLuaCode.IsEmpty()
		if !writeByMethod && fan.WriteRegister != 0 {
			add(fan.WriteRegister)
		}
	}
	for _, rw := range c.RegisterWriteConfigurations {
		out[rw.Register] = true
	}
	return out
}

// AcpiMethodsUsed returns the set of ACPI method paths this config
// invokes across every fan operation + every register-write side
// effect with `WriteMode = Call`. The set feeds the v0.8.0 PR B3
// ACPI bridge's closed-set discipline (RULE-NBFC-ACPI-01).
//
// A nil receiver returns nil. An empty set (the common case — only
// 7 of 311 configs use ACPI at all) means the ACPI bridge isn't
// required for this hardware.
func (c *Config) AcpiMethodsUsed() map[string]bool {
	if c == nil {
		return nil
	}
	out := make(map[string]bool)
	add := func(m string) {
		if s := strings.TrimSpace(m); s != "" {
			out[s] = true
		}
	}
	for _, fan := range c.FanConfigurations {
		add(fan.ReadAcpiMethod)
		add(fan.WriteAcpiMethod)
		add(fan.ResetAcpiMethod)
	}
	for _, rw := range c.RegisterWriteConfigurations {
		if strings.EqualFold(rw.WriteMode, "Call") {
			add(rw.AcpiMethod)
		}
		add(rw.ResetAcpiMethod)
	}
	return out
}

// UsesLua reports whether any fan operation or register-write
// configuration in this config invokes Lua. Used by the HAL probe
// to refuse Lua-driven configs in v0.8.0 (no Lua runtime).
func (c *Config) UsesLua() bool {
	if c == nil {
		return false
	}
	if len(c.LuaLibraries) > 0 {
		return true
	}
	for _, fan := range c.FanConfigurations {
		if !fan.ReadLuaCode.IsEmpty() || !fan.WriteLuaCode.IsEmpty() || !fan.ResetLuaCode.IsEmpty() {
			return true
		}
	}
	for _, rw := range c.RegisterWriteConfigurations {
		if strings.EqualFold(rw.WriteMode, "Lua") {
			return true
		}
		if !rw.LuaCode.IsEmpty() || !rw.ResetLuaCode.IsEmpty() {
			return true
		}
	}
	return false
}

// UsesACPI reports whether any fan operation or register-write
// configuration in this config invokes an ACPI method.
func (c *Config) UsesACPI() bool {
	return c != nil && len(c.AcpiMethodsUsed()) > 0
}
