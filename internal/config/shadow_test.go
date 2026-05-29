package config

import "testing"

// TestApplyShadow_ParsesAndShadowModeHelper covers the apply.shadow
// config surface (#1346): it round-trips from YAML, ShadowMode() reads
// it, and ShadowMode() is nil-safe so hot-path live-config readers can
// call it without a guard.
func TestApplyShadow_ParsesAndShadowModeHelper(t *testing.T) {
	t.Run("yaml_true", func(t *testing.T) {
		cfg, err := Parse([]byte("version: 1\napply:\n  shadow: true\n"))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if !cfg.Apply.Shadow {
			t.Errorf("apply.shadow did not parse to true")
		}
		if !cfg.ShadowMode() {
			t.Errorf("ShadowMode() = false, want true")
		}
	})

	t.Run("absent_defaults_false", func(t *testing.T) {
		cfg, err := Parse([]byte("version: 1\n"))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if cfg.ShadowMode() {
			t.Errorf("ShadowMode() = true with no apply section, want false")
		}
	})

	t.Run("nil_receiver_is_false", func(t *testing.T) {
		var cfg *Config
		if cfg.ShadowMode() {
			t.Errorf("(*Config)(nil).ShadowMode() = true, want false")
		}
	})
}
