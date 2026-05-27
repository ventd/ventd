package config

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"gopkg.in/yaml.v3"
)

// Save validates cfg, marshals it to YAML, and writes it atomically to path.
// Returns the validated config (with defaults applied) for swapping into the
// live pointer.
//
// EnrichChipName is called before marshal so every config produced via
// Save (web UI submissions, calibration completions, password updates)
// carries the chip identifier ResolveHwmonPaths needs on the next
// boot. Operators authoring config through the UI never have to touch
// the chip_name YAML field.
func Save(cfg *Config, path string) (*Config, error) {
	EnrichChipName(cfg)
	// Ensure every curve has its `_pct` fields populated before we
	// marshal. Callers that mutate cfg directly (the web UI's /api/config
	// write path, tests constructing CurveConfig{MinPWM: 30, ...}) won't
	// have set `_pct` themselves — without this call the Save would
	// emit a YAML that's missing the percent keys entirely.
	MigrateCurvePWMFields(cfg)
	// Build a shadow Config that carries only the `_pct` curve fields
	// in its Curves slice. The runtime cfg keeps legacy MinPWM /
	// MaxPWM / Value / PWM for every reader that has always used them
	// (buildCurve, validate); the YAML round-trip writes the percent
	// form and drops the legacy keys on every Save, so a Load→Save
	// cycle strips legacy lines from any pre-3f config in one pass.
	out := *cfg
	if len(cfg.Curves) > 0 {
		out.Curves = make([]CurveConfig, len(cfg.Curves))
		for i, c := range cfg.Curves {
			out.Curves[i] = c
			out.Curves[i].MinPWM = 0
			out.Curves[i].MaxPWM = 0
			out.Curves[i].Value = 0
			if len(c.Points) > 0 {
				pts := make([]CurvePoint, len(c.Points))
				copy(pts, c.Points)
				for j := range pts {
					pts[j].PWM = 0
				}
				out.Curves[i].Points = pts
			}
		}
	}
	data, err := yaml.Marshal(&out)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	validated, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	if err := writeFileSync(path, data, 0600); err != nil {
		return nil, err
	}
	return validated, nil
}

// SavePasswordHash writes a minimal config file containing only the web
// section with the given bcrypt password hash. Used during first boot, before
// the setup wizard has produced a full config. On next daemon start, the
// wizard's full config replaces this file.
func SavePasswordHash(hash, path string) error {
	minimal := Empty()
	minimal.Web.PasswordHash = hash
	data, err := yaml.Marshal(minimal)
	if err != nil {
		return fmt.Errorf("marshal minimal config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	return writeFileSync(path, data, 0600)
}

// writeFileSync writes data to path atomically (via a .tmp rename) and calls
// f.Sync() before the rename so the content survives an unclean reboot.
func writeFileSync(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("write config %s: %w", tmp, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write config %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("sync config %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close config %s: %w", tmp, err)
	}
	// When invoked as root (manual `sudo ventd ...` run, rescue/debug
	// session, etc.) match the tmp file's owner/group to the parent
	// config dir before the atomic rename. Without this, every save
	// by a root-euid process leaves root:root files in /etc/ventd,
	// and the systemd User=ventd service can no longer read its own
	// config on the next start. No-op when euid != 0.
	if os.Geteuid() == 0 {
		if info, err := os.Stat(filepath.Dir(path)); err == nil {
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				_ = os.Chown(tmp, int(st.Uid), int(st.Gid))
			}
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync() // best-effort; some filesystems don't support this
		_ = dir.Close()
	}
	return nil
}
