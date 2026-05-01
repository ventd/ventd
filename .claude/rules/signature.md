# Workload signature learning rules (v0.5.6)

These invariants govern v0.5.6's signature library. The library
turns the running process set into a stable, opaque, privacy-safe
label that v0.5.7 (Layer B), v0.5.8 (Layer C), and v0.5.9 (the
confidence-gated controller) all key per-(channel, signature) state
on. The privacy contract — comm-only hashing, per-install salt,
mode 0600, never in diag bundles — is the load-bearing reason
ventd can ship signature learning at all without operator
opt-in.

The patch spec is `specs/spec-v0_5_6-workload-signatures.md`.
The design of record is
`docs/research/r-bundle/R7-workload-signature-hash.md` (869 lines,
exhaustive). v0.5.6 transcribes R7; rules in this file restate R7's
locks.

## RULE-SIG-HASH-01: Hash MUST be SipHash-2-4 keyed by the per-install salt; output 64-bit, hex-rendered.

R7 §Q1 selects SipHash-2-4 over SHA-256/HMAC-SHA-256, BLAKE3, and
xxh3 because SipHash's design threat model — "attacker observes
attacker-known short inputs and their PRF outputs and cannot
recover the key" — exactly matches the leaked-diag-bundle threat
where comm names are publicly known software names. The
`github.com/dchest/siphash` library is the canonical Go
implementation; output is rendered as 16 lowercase hex chars per
RULE-OBS-PRIVACY-03's signature_label opacity invariant.

Bound: internal/signature/hash_test.go:TestHasher_Determinism
Bound: internal/signature/hash_test.go:TestHasher_OutputIs64BitHex

## RULE-SIG-HASH-02: Hash input MUST be /proc/PID/comm only; cmdline / exe / parent-comm forbidden.

R7 §Q3 rejects exe (leaks /home/$USER/... paths and Nix store
hashes), cmdline (leaks DB passwords passed via -p, file paths,
URLs), and parent-comm (varies between desktop launcher, terminal,
systemd-run, Steam, VS Code terminal — produces more flap, not
less). comm is the kernel-canonicalised 16-byte name, set by the
binary itself, with bounded entropy and zero per-install variance.
The hasher's API surface only exposes HashComm; there is no
HashCmdline or HashExe by construction.

Bound: internal/signature/hash_test.go:TestHasher_DeterministicAcrossRestarts

## RULE-SIG-SALT-01: Salt file MUST be 32 random bytes at /var/lib/ventd/.signature_salt, mode 0600.

R7 §Q6 specifies 32 bytes from crypto/rand at the fixed path with
ventd:ventd ownership and mode 0600. The first 16 bytes are the
SipHash key; the remaining 16 are reserved for future per-channel
HKDF derivation. LoadOrCreateSalt enforces the permission gate at
daemon start — a salt file with mode > 0600 returns an error and
the daemon refuses to start until the operator chmods it.

Bound: internal/signature/hash_test.go:TestSalt_FilePermissionsAre0600
Bound: internal/signature/hash_test.go:TestSalt_LengthIs32Bytes
Bound: internal/signature/hash_test.go:TestSalt_RejectsLooseFilePermissions

## RULE-SIG-SALT-02: Salt MUST be excluded from diag bundles and never logged.

The salt is the cryptographic key that protects every signature
hash in the library; if it leaks, every persisted label becomes
rainbow-table-reversible to its plaintext comm. The P9 redactor's
existing exclusion list covers /var/lib/ventd/.signature_salt by
filename; the signature package never emits salt bytes to its
logger. Operators who genuinely need to share a deterministic-
replay bundle use --include-salt (deferred to v0.5.10's doctor)
which prints a five-line warning before consenting.

Bound: internal/signature/hash_test.go:TestSaltKey_DifferentSaltDifferentLabels

## RULE-SIG-SALT-03: Missing salt file MUST trigger fresh-salt regeneration.

LoadOrCreateSalt regenerates a fresh 32-byte salt via crypto/rand
when the file is absent, writes it atomically (tmpfile + rename),
and returns the bytes. Existing KV signature/<label> entries become
hash-tuple-stale (the new salt produces different hashes for the
same comms), but their RLS state is recoverable: the new tick
labels collide with old buckets eventually via LRU. The operator-
visible failure mode is "signature labels reset, Layer C
re-converges over ~24 hours" — recoverable, not catastrophic.

Bound: internal/signature/hash_test.go:TestSalt_RegenerationOnMissingFile

## RULE-SIG-LIB-01: Contribution gate MUST be EWMA-CPU > 5% of one core OR RSS > 256 MiB; kthreads excluded.

R7 §Q2 calibrates the gate to exclude the always-running daemon
tail (systemd, dbus-daemon, NetworkManager, pipewire-resolved,
plasmashell at idle) on every fleet member. Kthreads (PPid==2 OR
comm starts with '[') are excluded unconditionally because their
comm names like kworker/u32:1+events_unbound are PID-suffixed and
flap heavily in the workload signature without representing a
real workload.

Bound: internal/signature/library_test.go:TestLibrary_GateRejectsBelowThresholds
Bound: internal/signature/library_test.go:TestLibrary_KthreadFilter

## RULE-SIG-LIB-02: Signature label MUST be the top-K=4 hashes by EWMA weight, sorted lexicographically, '|'-joined, max 80 chars.

R7 §Q4 selects K=4 to capture the dominant-binary plus 1-3
supporting binaries pattern observed across all four flap
scenarios (Steam launch, kernel build, Chrome Site Isolation, no-op
systemd-resolved). The lexicographic sort canonicalises the label
so insertion order doesn't matter; the 80-char cap bounds
persistence overhead and matches the schema doc §2.1
signature_label string field's intent.

Bound: internal/signature/library_test.go:TestLibrary_TopKByWeight

## RULE-SIG-LIB-03: K-stable promotion MUST require M=3 consecutive ticks of identical top-K.

R7 §Q4 sets M=3 ticks at 2 s each = 6 seconds of stability before
a label change becomes the active signature. This is short enough
to pick up real transitions (game launch → game steady state) and
long enough to filter compile-storm reshuffles where the top-K
flips sample-to-sample but the underlying workload is one
"compile" task.

Bound: internal/signature/library_test.go:TestLibrary_KStablePromotionRequires3Ticks

## RULE-SIG-LIB-04: EWMA half-life MUST be 2 seconds.

R7 §Q4 matches the half-life to R11's fast-loop tick. At 2 s
half-life, contributions decay to <1% of their initial weight in
~14 seconds; transient processes (Steam launch services.exe,
kernel build cc1 instances that exit) effectively disappear from
the multiset within one M=3 promotion gate. The decay factor per
tick is 0.5^(dt/half_life). The test seeds a heavy single-process
multiset, advances exactly one half-life with no further
contributions, and asserts the weight halves within ±10%.

Bound: internal/signature/library_test.go:TestLibrary_EWMAHalfLifeDecaysCorrectly

## RULE-SIG-LIB-05: Library MUST be capped at 128 buckets with weighted-LRU eviction.

R7 §Q5 calibrates 128 buckets against a realistic per-user
workload taxonomy: ~25-40 distinct signatures in active use plus a
long tail of one-off launches bringing the lifetime total to ~60-90
over a year. The eviction score is HitCount × exp(-(age/τ)) with
τ=14 days; a frequently-hit workload that the user happens not to
hit for two weeks does not lose its bucket to a single transient
launch.

Bound: internal/signature/library_test.go:TestLibrary_BucketCountCapAt128

## RULE-SIG-LIB-06: Maintenance-class processes dominating top-K MUST emit reserved label maint/<canonical-name>.

R7 §Q2 (B) reuses R5's idle-gate process blocklist (rsync,
plex-transcoder, ffmpeg, make, apt, dpkg, dnf, pacman, ...) as a
positive-label dictionary. When one of these processes dominates
the K=4 set (their EWMA weight ≥ 2× the median of the others),
the signature label is overridden from the hash-tuple to
maint/<canonical>. This collapses the long tail of "maintenance
+ something tiny" combinations into a small, stable set of
reusable buckets and gives Layer C explicitly recognisable
categories for its RLS state. R5's blocklist is the single source
of truth; signature/blocklist.go re-exports the read API.

Bound: internal/signature/library_test.go:TestLibrary_PlexTranscoderEmitsMaintLabel

## RULE-SIG-LIB-07: Disabled config MUST emit fixed label fallback/disabled and never write to KV.

The library is started in the permanent-disabled state when (a)
R1 reports the daemon is in a Tier-2-BLOCK container or VM, (b) R3
reports HardwareRefused (Steam Deck etc.), or (c) the operator
toggle Config.SignatureLearningDisabled is true. In any of these
cases Tick is a no-op that returns the fixed label
"fallback/disabled" and persistence is suppressed — no KV writes,
no salt file generation if absent, no /proc walks.

Bound: internal/signature/library_test.go:TestLibrary_DisabledEmitsFallback

## RULE-SIG-LIB-08: User toggle MUST behave identically to R1/R3 disable paths.

The Config.SignatureLearningDisabled toggle is the operator's
primary off-switch and is exposed in Settings → Smart mode per
spec-12 amendment §3.5. When set, the library transitions to the
disabled state on the next Tick; existing in-memory state is
preserved (the toggle is "stop learning new things," not "wipe
what was learned"). Flipping back to false resumes learning from
the preserved state. The test exercises ApplyDisableGate with
DisableReasonOperatorToggle to mirror what main.go does when
reading Config.SignatureLearningDisabled.

Bound: internal/signature/library_test.go:TestLibrary_HonoursToggleOff

## RULE-SIG-PERSIST-01: Persistence MUST use spec-16 KV under namespace signature.

R7 §Q5 selects KV over append-log (would conflate creation events
with state updates) and over blob (would force whole-library
rewrites on every tick). The Bucket struct is msgpack-encoded with
{Version, HashAlg, LabelKind, RLSState, FirstSeenUnix,
LastSeenUnix, HitCount, CurrentEWMA}. Save iterates the in-memory
bucket map; LoadLabels reads from a namespace-manifest stored at
KVNamespace="signature_manifest", key="labels".

Bound: internal/signature/persistence_test.go:TestPersistence_KVRoundTrip

## RULE-SIG-PERSIST-02: Daemon start MUST resume bucket state from KV without losing prior counters.

LoadLabels populates the in-memory bucket map from KV on daemon
start. Each bucket's HitCount, LastSeenUnix, and CurrentEWMA
persist across the restart. The live multiset starts empty and
warms up over the first few ticks; only the most recently active
bucket's CurrentEWMA could in principle be re-seeded into the
multiset, but R7 deliberately leaves this for future revision —
warm-restart from the empty multiset converges within ~6 seconds
(M=3 × 2-second tick) anyway.

Bound: internal/signature/persistence_test.go:TestLibrary_WarmRestartFromKV

## RULE-SIG-CTRL-02: Library.Label() MUST be lock-free for the controller hot loop.

The controller stamps every observation Record with the current
signature label; reading Label() must not block on the signature
tick goroutine's mutex. The library publishes the label via
atomic.Pointer[string]; Label() loads the pointer without
acquiring lib.mu. This decouples the signature tick (every 2 s)
from the controller hot loop (every poll interval, typically 0.5
Hz) and protects against tail-latency in the signature path
spilling into the control path.

Bound: internal/signature/library_test.go:TestLibrary_LabelReadIsLockFree
