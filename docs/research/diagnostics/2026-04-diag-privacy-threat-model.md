# 2026-04 Diagnostic Bundle Privacy Threat Model

**Status:** research doc, drives `internal/diag/redactor/` design and PR 2c default-conservative redaction profile.
**Scope:** what can leak through a ventd diagnostic bundle, what attacks the leakage enables, and the redaction primitives + defaults that mitigate each vector.
**Out of scope:** trace harness output (separate threat surface, much larger; spec-05-prep handles), network-protocol-level threats (no exfil channel exists; ventd produces local files only), supply-chain threats to ventd itself.
**Cross-refs:** spec-03 amendment §16 (mandates redaction), diag bundle prior art §10–11 (redaction report + defaults), GPU vendor catalog §5.7.

---

## 1. Threat-modeling frame

### 1.1 What we're protecting

A ventd diagnostic bundle is a tarball produced by `ventd diag bundle` and shared by the user with a third party — typically a GitHub issue, a forum post, a chat message to a friend, or a vendor support case. The bundle's social purpose is **maximally informative** to a sympathetic recipient who is debugging the user's problem.

Privacy is the constraint that prevents that social purpose from also enabling hostile use.

### 1.2 What we're protecting against

Three plausible adversaries:

- **A1: Public-attachment doxxer.** User posts bundle to a public GitHub issue. Anyone reading the issue (including search-indexed bots) can extract identifying or correlatable data.
- **A2: Compromised recipient.** User sends bundle to vendor support; vendor's case-tracking system gets breached. Bundle contents are exposed in a dump.
- **A3: Adversarial recipient.** User shares bundle with someone they later cease to trust. Recipient retains the bundle and can use its contents to identify, track, or attack the user.

What we are NOT defending against:

- **Local attacker with root** on the user's machine. They already have everything; the bundle adds nothing.
- **Targeted forensic analysis** with prior knowledge of the user. If A3 already knows the user's general hardware and city, redaction provides limited additional protection. The threat model focuses on the marginal disclosure, not absolute anonymity.
- **Statistical de-anonymization** by combining many redacted bundles. Out of scope; ventd does not ship a bundle aggregator.

### 1.3 Trust assumption

The user trusts ventd's redactor to do what it claims. To make that trust auditable rather than blind, every bundle includes `REDACTION_REPORT.json` (per diag bundle prior art §11.3) listing what was changed. User can review, and if they don't trust the redactor, they can manually inspect the bundle before sharing.

---

## 2. Leak inventory by data class

Each row: data class, where it appears, what it enables an adversary to do, current state (leaks-by-default if unredacted).

### 2.1 Direct identifiers

| Class | Captured at | Adversary use | Leakage if unredacted |
|---|---|---|---|
| Hostname (FQDN) | `commands/system/uname_-a`, `commands/journal/*` (in `_HOSTNAME` field), `etc/hostname`, kernel cmdline | A1: searchable; A2/A3: link bundle to a specific machine | High — appears in 30+ places per bundle |
| DMI system-uuid | `commands/system/dmidecode_-t_system`, `/sys/class/dmi/id/product_uuid` | A2/A3: stable cross-bundle correlation, often exfiltrated by malware as machine ID | High — single value, easy to grep |
| DMI system-serial-number | `dmidecode -t system` | A2/A3: warranty-database correlation, vendor account linkage | High |
| DMI baseboard-serial-number | `dmidecode -t baseboard` | Same as system-serial-number; sometimes more unique | High |
| DMI chassis-serial-number | `dmidecode -t chassis` | Same; sometimes asset-tag-style organization-scoped | High |
| DMI asset-tag (system, baseboard, chassis) | `dmidecode` various | Often org-internal asset ID — links bundle to a workplace | Medium — frequently "Not Specified" but occasionally org-meaningful |
| machine-id (`/etc/machine-id`) | only if explicitly captured | A2/A3: stable systemd-level identifier, used by some apps as a tracking token | Currently NOT captured; do not start |

### 2.2 Network identifiers

| Class | Captured at | Adversary use | Leakage if unredacted |
|---|---|---|---|
| MAC addresses | not directly captured by ventd, but appear in `dmesg`, journal, USB physical paths | A1: hardware fingerprint persists across reboots, links across reinstalls | Medium — depends on what filtered logs include |
| IPv4 / IPv6 | journal entries (e.g., from sshd if user pastes config) | A1: geolocation, correlation with other public IP datasets | Low if journal is filtered to ventd unit only; spec-04-relevant logs unlikely to contain network config |
| DHCP-style hostname (e.g., `dhcp-192-168-0-66`) | journal, hostname capture | A1: leaks the IP through the hostname even after IP redaction | Medium — sosreport had a public bug here ([sos#3388](https://github.com/sosreport/sos/issues/3388)) |

### 2.3 User identifiers

| Class | Captured at | Adversary use | Leakage if unredacted |
|---|---|---|---|
| Username (login name) | journal `_UID`/`_AUDIT_LOGINUID`, kernel cmdline if `user=` param, paths under `/home/<user>/` | A1: real-name correlation if username is a real name; A2/A3: ties activity to a person | Medium — only present if user-mode tooling is captured |
| User home path (`/home/<user>`) | only if user passed `--profile ~/...` to ventd | Same as username | Low — only when user invoked ventd with a home-relative path |
| User group memberships | not captured by ventd | n/a | None — out of scope |

### 2.4 Hardware fingerprint (legitimate)

| Class | Captured at | Adversary use | Leakage if unredacted |
|---|---|---|---|
| Manufacturer + model name | DMI baseboard, system; `lspci` | A1: narrows population to a hardware class; this is desirable for support, low marginal harm | None — keep |
| BIOS vendor + version + release date | DMI BIOS | Same — narrows but doesn't identify | None — keep |
| CPU model | `/proc/cpuinfo` | Same | None — keep |
| Memory total | `free` | Adversary use trivial; bundle context already implies "PC user" | None — keep |
| Kernel version | `uname -r` | Adversary use trivial | None — keep |
| Distro identifier | `/etc/os-release` ID + VERSION_ID | Same | None — keep |
| Distro PRETTY_NAME | `/etc/os-release` | Sometimes contains custom build name including hostname | Medium — strip if it deviates from standard format |
| GPU model + UUID | NVML, AMDGPU | NVML GPU UUID is stable across reboots, can correlate; GPU model is generic | Medium for GPU UUID, none for model |
| USB physical path | hidraw devinfo, journal | Topology fingerprint stable across reboots; encodes USB tree structure | Medium — preserve topology shape, redact specific bus/port numbers |

### 2.5 Operational state (no immediate identifier risk, but could leak intent)

| Class | Captured at | Adversary use | Leakage if unredacted |
|---|---|---|---|
| Running processes (filtered to fan-control-related) | `commands/userspace/processes_with_hidraw_open` | Reveals what other software runs (LACT, GreenWithEnvy, CoolerControl, gaming clients) | Low — limited categorical leak, expected for a fan-controller diag |
| User's profile file content | `state/runtime/active_profile.yaml` | Contains user-named devices or labels (e.g., "Phoenix's-PC-CPU-fan") | Medium — user-supplied free-form labels can leak |
| Calibration history with timestamps | `state/calibration/history/*` | Reveals usage patterns (when machine is used) | Low — timestamps can be coarsened |
| BIOS version captured in calibration | calibration JSON | Same as DMI BIOS version, but in a second location | Already covered by §2.4 |
| Kernel cmdline | `/proc/cmdline` | Often contains crashkernel, root UUID, hostname args | High — must redact root=UUID, root=PARTUUID, ip=, hostname= |

### 2.6 Things ventd should NEVER capture (even with `--no-redact`)

These have no diagnostic value and high disclosure potential. Capture path must not exist.

- `/etc/shadow`, `/etc/sudoers`, `/etc/sudoers.d/*`
- SSH keys (`/root/.ssh/`, `/home/*/.ssh/`)
- TLS keys / certificates with private material
- `/etc/passwd` (already public per UNIX convention but no fan-control reason to capture)
- D-Bus credentials / session tokens
- systemd credentials (`/run/credentials/*`)
- kernel keyring contents (`/proc/keys`)
- TPM contents
- `/proc/<pid>/environ` for any process
- Shell history files
- Browser data, mail spools, ~/Documents, ~/Desktop
- Bluetooth pairing keys
- network manager configs containing PSKs

This is enforced architecturally: the bundle generator's allowlist of capture paths must not include these. **No redactor is asked to scrub them, because they are never collected in the first place.**

---

## 3. Redactor design

### 3.1 Three redaction profiles

| Profile | Default | Use case |
|---|---|---|
| **default-conservative** | yes | Public attachment (GitHub, forum). Strips all §2.1–2.3 identifiers. |
| **trusted-recipient** | opt-in | Vendor support case where the user has a contractual relationship and wants more detail preserved. Strips only §2.1 direct identifiers (UUIDs, serials), keeps hostname for case-correlation. |
| **off** | opt-in via `--no-redact` | User's own debug runs, never to be shared. CLI prints loud warning at completion. |

CLI flags:

```
ventd diag bundle                        # default-conservative
ventd diag bundle --redact=trusted       # trusted-recipient
ventd diag bundle --redact=off           # warning-required, plain capture
ventd diag bundle --redact=off --i-understand-this-is-not-redacted   # actual no-redact, no prompt
```

`--redact=off` without the long flag is interactive: prompts user to type a confirmation phrase (e.g., type the word `confirm`). This isn't security, it's a deliberate-action ratchet so users don't accidentally produce un-redacted bundles.

### 3.2 Redactor primitives (per data class)

Each primitive: input matcher, output replacement, consistency rule.

#### P1: Hostname redactor
- Input matcher: literal hostname (read once at bundle-start), plus all reverse-DNS variants (FQDN, short name, DHCP `dhcp-W-X-Y-Z` form per [sos#3388](https://github.com/sosreport/sos/issues/3388))
- Output: `obf_host_1`, `obf_host_2`, ... (incrementing in the rare case of multiple captured hosts)
- Consistency: same input → same token throughout the bundle and across runs (mapping persisted to `~/.local/state/ventd/redactor-mapping.json`, root-mode persisted to `/var/lib/ventd/redactor-mapping.json`)

#### P2: DMI identifier redactor
- Input matcher: regex on `Serial Number:` and `UUID:` and `Asset Tag:` lines in dmidecode output; AND specific `/sys/class/dmi/id/product_uuid`, `/sys/class/dmi/id/product_serial`, `/sys/class/dmi/id/board_serial`, `/sys/class/dmi/id/chassis_serial`, `/sys/class/dmi/id/board_asset_tag`, `/sys/class/dmi/id/chassis_asset_tag`
- Output: literal string `[REDACTED:DMI_SERIAL]` etc. Not consistent-mapped — these are one-shot fingerprints, no analytical value in preserving correlation
- Special case: ventd's calibration JSON stores a 16-hex truncated SHA-256 of the DMI baseboard fingerprint (per spec-03 amendment §11). That truncated hash is already a proxy that doesn't recover the original; **bundle keeps it as-is** because it's how ventd identifies hardware in the field

#### P3: MAC address redactor
- Input matcher: regex `\b([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}\b`
- Output: `aa:bb:cc:dd:ee:NN` where NN is consistent-mapped (so two bundle entries referencing the same NIC stay correlated within a bundle)
- Skip: broadcast `ff:ff:ff:ff:ff:ff` and zero `00:00:00:00:00:00` are kept as-is (not real identifiers)

#### P4: IP address redactor
- Input matcher: IPv4 regex with CIDR support; IPv6 regex
- Output: consistent-mapped to `100.64.X.Y` (RFC 6598 carrier-grade NAT space, unmistakably synthetic) and `fd00:obf::N` for IPv6
- Topology preservation: subnet relationships preserved within a bundle (192.168.1.5 and 192.168.1.6 → 100.64.1.5 and 100.64.1.6)
- Skip: 127.0.0.1, ::1, 0.0.0.0, link-local fe80::/10 — kept as-is

#### P5: Username redactor
- Input matcher: read `getent passwd` to identify all login names with UID >= 1000; build literal-match list
- Output: `obf_user_1`, `obf_user_2`, ...
- Skip well-known system users: `root`, `nobody`, `daemon`, `bin`, `systemd-*`, `messagebus`, etc. (use `/etc/login.defs` UID_MIN / SYS_UID_MAX as bounds)

#### P6: Path redactor
- Input matcher: `/home/<obf_user>/` and any path containing a redacted username
- Output: `/home/<obf_user>/`
- Special: `/root/` paths in `--redact=default-conservative` mode are kept (system path, no user PII), but specific files within are not captured per §2.6 architectural exclusion

#### P7: USB physical path redactor
- Input matcher: `usb-X-Y-Z.W:U.V` style strings in hidraw devinfo, journal
- Output: preserve depth (number of dashes) but replace numbers with letters (`usb-A-B-C.D:E.F`)
- Reason: USB topology shape can be diagnostically meaningful (hub vs root port), but specific bus IDs are ephemeral identifiers

#### P8: Kernel cmdline redactor
- Input matcher: tokens matching `root=UUID=...`, `root=PARTUUID=...`, `cryptdevice=UUID=...`, `ip=...`, `hostname=...`, `BOOT_IMAGE=...` (path strip)
- Output: token-by-token replacement with `root=UUID=[REDACTED]` etc.
- Keep: kernel parameters affecting fan/hwmon (`amdgpu.ppfeaturemask=...`, `intel_pstate=...`, `nvme.poll_queues=...`)

#### P9: User-supplied label redactor
- Input matcher: free-form labels in `state/runtime/active_profile.yaml` — fan names, profile names, comment fields
- Output: `[REDACTED:USER_LABEL_<n>]`
- This is the most aggressive redaction because user-supplied strings are unpredictable. Rationale: a user labeling their fan "Phoenix-CPU-cooler" leaks the user's name; a user labeling the same fan "front-intake-1" doesn't. The redactor cannot tell, so default-conservative redacts all user-labels and lists them in REDACTION_REPORT.json so the user can selectively unredact

#### P10: User-supplied keyword redactor
- Input matcher: per-bundle `--redact-keyword=foo,bar,baz` flag
- Output: `obf_keyword_<n>`
- Use case: user knows their bundle contains a specific string they want scrubbed (e.g., a project codename in a journal log)

### 3.3 Mapping persistence

Per `sos clean` precedent: consistent obfuscation mappings persist to disk so subsequent bundles use the same obfuscation tokens. This is good — it lets a support engineer correlate two bundles from the same user across sessions.

**However, this means the mapping file is itself a privacy artifact.** It contains the cleartext-to-obfuscated mapping, i.e., the de-redaction key. Storage:

- root-mode: `/var/lib/ventd/redactor-mapping.json`, mode 0600, owned root:root
- user-mode: `$XDG_STATE_HOME/ventd/redactor-mapping.json`, mode 0600, owned user
- `ventd diag bundle --reset-redactor-mapping` deletes this file (loses cross-bundle correlation; user opts in)
- `ventd diag bundle --no-mapping` runs without persisting the new mapping

### 3.4 Redactor verification

Each bundle includes `REDACTION_REPORT.json` (per diag bundle prior art §11.3). Schema:

```json
{
  "redactor_version": 1,
  "redactor_profile": "default-conservative",
  "redactions_by_class": {
    "hostname": 12,
    "dmi_serial": 4,
    "dmi_uuid": 1,
    "mac_address": 8,
    "ipv4": 3,
    "ipv6": 0,
    "username": 0,
    "user_path_home": 1,
    "usb_physical_path": 2,
    "kernel_cmdline_token": 3,
    "user_label": 5,
    "user_keyword": 0
  },
  "redactions_skipped_classes": [],
  "non_redacted_files": [
    "manifest.json",
    "REDACTION_REPORT.json",
    "commands/ventd/version"
  ],
  "redaction_consistent": true,
  "warnings": []
}
```

`redaction_consistent: true` is set after a self-check pass: redactor walks the final bundle searching for any of the literal cleartext values it should have redacted. If any are found → `redaction_consistent: false`, warnings populated, bundle generation fails with a hard error (not a warning — opt-out via `--allow-redaction-failures` or `--redact=off`).

### 3.5 Known gaps (residual disclosure surface)

These are real but accepted-as-low-risk:

- **G1: Distinctive hardware combinations.** "RTX 4090 + 13900K + Phanteks 14 fans + 192 GB DDR5" is a fingerprint even with all identifiers redacted. ventd does not distort hardware data because that destroys diagnostic value. Users with unusual setups should know their bundle is partially identifying by hardware shape alone.
- **G2: Free-form text in journal.** Even with username + hostname + path redaction, journal MESSAGE fields can contain anything the daemon emitted. ventd controls its own log format and does not log free-form user input, but third-party-written kernel messages (rare, fan-control-relevant) might. Default: ventd's bundle includes only ventd's own unit journal, no other unit.
- **G3: Timing fingerprinting.** Bundle timestamps reveal when the user ran `ventd diag bundle`. Within the bundle, calibration timestamps reveal when the user calibrated. These are usage-pattern leaks. Redactor does not coarsen timestamps because that would defeat trace replay. User can run `ventd diag bundle --strip-timestamps` to convert all timestamps to monotonic-clock-relative; trace replay still works, but cross-bundle correlation degrades.
- **G4: BIOS / firmware version is a unique-ish fingerprint** when combined with hardware model. e.g., "this exact ASUS Z790-A board with BIOS 1605 dated 2024-03" is a small population. Kept for diagnostic value; not a defended class.
- **G5: ventd version itself** is captured. Not a privacy leak per se — it is needed for support — but combined with timestamp it tells an adversary how recently the user updated. Accepted.

---

## 4. Threat-actor mapping

### 4.1 Against A1 (public attachment doxxer)

Mitigated by: P1 (hostname), P2 (DMI), P3 (MAC), P4 (IP), P5 (username), P6 (home path), P7 (USB phys path), P9 (user labels). Together these strip every direct identifier from a default-conservative bundle.

Residual: G1 (distinctive hardware), G3 (timing). User retains responsibility for sharing decisions when their hardware is unusual.

### 4.2 Against A2 (compromised recipient)

Same mitigations as A1. Trusted-recipient profile (which keeps hostname) widens this surface; user makes the trust decision when they choose `--redact=trusted`.

Mapping file is local, doesn't go in the bundle, so a compromised recipient cannot reverse the obfuscation even with multiple bundles from the same user (unless they have unrelated cleartext to correlate against, in which case they didn't need the mapping).

### 4.3 Against A3 (adversarial recipient)

Same as A1, plus: user can run `ventd diag bundle --reset-redactor-mapping` before each new bundle to deny the recipient cross-bundle correlation. Trade-off: support engineer working over multiple sessions has to re-correlate manually.

---

## 5. Comparison with sosreport (`sos clean`)

| Property | sos clean | ventd redactor |
|---|---|---|
| Default | opt-in (`--clean` flag) | opt-out (default-conservative on) |
| Audit report | mapping file only (technical) | `REDACTION_REPORT.json` summarizing classes + counts |
| Self-check pass | not documented | mandatory; bundle generation fails on detected leaks |
| User-supplied keywords | yes (`--keywords`) | yes (P10) |
| Domain/subdomain handling | yes (`--domains`) | yes (folded into P1 hostname) |
| Configurable parser disable | yes | yes (`--disable-redactor-class hostname,ip,...`) |
| Architectural exclusions | implicit (plugin-based) | explicit (capture allowlist, §2.6) |
| Mapping persistence | yes, default-on | yes, default-on, with `--no-mapping` opt-out |
| Public-attachment safety | requires user opt-in | safe by default |

The single biggest divergence — defaulting redaction ON — is the lesson learned from sosreport's public history of accidentally-non-cleaned attachments.

---

## 6. CoolerControl, liquidctl debug, NVIDIA bug-report comparison

| Tool | Default redaction | What leaks unredacted |
|---|---|---|
| CoolerControl export | none | full daemon log including hostname, all sysfs paths verbatim |
| `liquidctl --debug` | none | hostname (in path), USB physical path, sometimes serial numbers, raw HID packets |
| `nvidia-bug-report.sh` | none | dmesg (lots of kernel-side identifiers), Xorg log, full DMI, hostname, IPs from network logs |

**ventd's redactor is more conservative than any of these** by default. This is appropriate: ventd is the only one whose bundle is designed for casual public attachment to GitHub issues by users who don't read the bundle before posting.

---

## 7. Implementation notes for PR 2c

### 7.1 Package layout

```
internal/diag/redactor/
├── doc.go                  # this threat model summarized
├── profiles.go             # default-conservative, trusted-recipient, off
├── primitive.go            # Primitive interface
├── hostname.go             # P1
├── dmi.go                  # P2
├── mac.go                  # P3
├── ip.go                   # P4
├── username.go             # P5
├── path.go                 # P6
├── usb_physical.go         # P7
├── cmdline.go              # P8
├── user_label.go           # P9
├── user_keyword.go         # P10
├── mapping.go              # persistence
├── self_check.go           # post-bundle self-verification pass
├── report.go               # REDACTION_REPORT.json writer
└── redactor_test.go        # one subtest per primitive + integration
```

Each primitive: `func (p *Primitive) Redact(ctx, content []byte) (redacted []byte, count int, err error)`. Pure function, no shared state except the mapping store passed in.

### 7.2 RULE-DIAG-PR2C-* invariants (proposed; PR 2c adds these)

- `RULE-DIAG-PR2C-01`: Default profile is `default-conservative`. Subtest verifies CLI default.
- `RULE-DIAG-PR2C-02`: Self-check pass detects un-redacted hostname strings in final bundle.
- `RULE-DIAG-PR2C-03`: Self-check failure is fatal unless `--allow-redaction-failures` is passed.
- `RULE-DIAG-PR2C-04`: Mapping file has 0600 perms.
- `RULE-DIAG-PR2C-05`: `--redact=off` requires either interactive confirm or `--i-understand-this-is-not-redacted` flag.
- `RULE-DIAG-PR2C-06`: Architecturally-excluded paths (§2.6 list) are never read or captured, even with `--no-redact`.
- `RULE-DIAG-PR2C-07`: REDACTION_REPORT.json is generated for every bundle, including `--redact=off` bundles (where it lists every class as 0).
- `RULE-DIAG-PR2C-08`: Mapping is consistent within a bundle (same input → same output).
- `RULE-DIAG-PR2C-09`: Reading mapping file from another machine does not crash (graceful schema-mismatch handling).

Subtests in `redactor_test.go` bind 1:1 to these.

### 7.3 Test fixtures

`testdata/redactor/` ships:
- `synthetic-dmi-output.txt` — with all identifier types in known positions
- `synthetic-journal-with-host.ndjson` — 50 entries containing hostname, username, MAC, IP variants
- `synthetic-cmdline-with-uuid` — boot cmdline with `root=UUID=...`
- `synthetic-active-profile.yaml` — user-labeled profile for P9 testing
- `expected-redacted-output.txt` for each — golden output

Subtests assert exact match between redacted output and golden. CI catches regressions.

---

## 8. Out of scope for PR 2c

1. **Encryption of the bundle at rest** (e.g., GPG-encrypted output). Cockpit web console offers this for sosreport; CLI users typically don't need it. Defer.
2. **Cryptographic deniability** (provable that the bundle came from a specific machine). Not a redaction concern; not a feature.
3. **Differential privacy** over multiple bundles. Out of scope; ventd doesn't aggregate.
4. **Full content review pre-share** (interactive "show me what's in the bundle" step). User can `tar -tzf` the bundle themselves; ventd doesn't add a viewer.
5. **Metadata-stripping for binary files.** Bundle contains no binary attachments that would have EXIF-style metadata. If future versions add screenshot capture, revisit.
6. **Kernel-side redaction.** All redaction is userspace post-capture. A targeted kernel module that stripped identifiers at log-emission time would be more comprehensive but is wildly out of scope.

---

## 9. Open questions for chat 3 / Phoenix

- **Q1:** Default user-keyword behaviour — empty list, or attempt to auto-derive keywords from the user's profile name and hostname-prefix variants? **Recommendation: empty default, document in CLI help that users can opt-in to extra keywords.**
- **Q2:** Should `REDACTION_REPORT.json` contain SHA-256 hashes of the original cleartext (so the user can later verify "was X in the bundle" without re-running)? **Recommendation: no — adds attack surface (hash + known-plaintext = reversal); user already has the mapping file locally.**
- **Q3:** Mapping file format — JSON or NDJSON? **Recommendation: JSON object (`{cleartext: token}`); small files, simpler than NDJSON for this case.**
- **Q4:** Should the redactor handle multi-byte / non-ASCII strings (e.g., a hostname in CJK)? **Recommendation: yes — Go's regex supports Unicode by default; add CJK fixture in testdata to lock behavior.**

---

## 10. Cross-cuts

- **spec-03 amendment §16** (mandates redaction) — this doc is the implementation spec for that mandate.
- **diag bundle prior art §11.3** (REDACTION_REPORT.json) — schema specified here, generator lives in `internal/diag/redactor/report.go`.
- **spec-05-prep §13** (output dir by-uid) — same pattern applies for the mapping file.
- **GPU vendor catalog §5.7** (capture items) — those items go through the redactor like everything else; NVML UUIDs are redacted under P2 (DMI-class identifier handling extended to GPU UUIDs).

---

**End of diagnostic bundle privacy threat model.**
