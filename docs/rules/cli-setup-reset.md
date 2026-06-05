# CLI: lost-password reset

## RULE-CLI-SETUP-RESET: `ventd setup --reset` clears the admin password and nothing else.

The login screen has always offered "Forgot the password? Run `ventd setup
--reset`" — but until #1504 no such command existed: the positional `setup`
was silently ignored by the flag parser and the binary tried to start the
daemon. The subcommand now exists and its scope is deliberately
password-only: forgetting a password must not cost a calibrated setup.

`runSetupReset(configPath, authPath, stateDir, restart, stdout, stderr)`:

1. Removes `auth.json` **and** `auth.json.bak` — both hold the forgotten
   hash (the `.bak` is authpersist.Save's pre-overwrite backup).
2. Scrubs a legacy `web.password_hash` from `config.yaml` if one is still
   present. This is load-bearing, not cosmetic: with `auth.json` gone,
   `migrateAuthToFile` treats a config-resident hash as pre-migration state
   and writes it straight back into a fresh `auth.json` on the next start —
   resurrecting the password the operator can't remember. A scrub failure
   is therefore a hard error, not a warning. A config that fails to load
   is safe to skip (the migration can't read it either).
3. Restarts the daemon (`systemctl try-restart`) only when the pidfile
   shows a live process (`state.RunningPID`, read-only) — the running
   daemon holds the hash in memory (`authHashValue`), so without a restart
   the reset would only take effect at some future reboot.

Everything else — `config.yaml` fan/curve content, calibration KV, the
applied marker, smart-mode state — is untouched. On the next start the
daemon's existing lost-credentials integrity guard (config present, no
auth hash → first-boot fallback) re-opens the wizard's password-set step,
and because the hash is empty a fresh one-time setup token is minted for
non-loopback enrolment, exactly as on a fresh install. The full-wipe paths
remain the web surfaces: `POST /api/setup/reset` (config + wizard state,
keeps auth) and `POST /api/admin/factory-reset` (everything).

The subcommand is dispatched positionally before the global `flag.Parse`,
so `ventd setup <anything>` can never again fall through to daemon
startup. Root is required (auth.json is root-owned 0600); the command
refuses early with a sudo hint rather than half-failing on file
permissions.

Bound: cmd/ventd/setup_reset_test.go:TestRunSetupReset
Bound: cmd/ventd/setup_reset_test.go:TestRunSetupResetCLI_Usage
