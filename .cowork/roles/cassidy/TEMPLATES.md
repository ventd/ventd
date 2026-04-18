# Cassidy issue templates

Pre-filled skeletons for the bug classes that keep recurring. Using these shortens my issue bodies and gives Atlas a consistent shape to queue from.

Each template has:

- **Use when** — the diff signal that triggers this template
- **Body skeleton** — the minimum-viable issue body
- **Fields to fill** — always PR #, file:line, one failure-mode sentence, one recommended fix

Default discipline: one recommended fix. Only expand into a two-option menu if the choice genuinely needs human judgment (e.g. a behavioural trade-off Atlas has to pick between). Never a three-option menu — that was my old bad habit; Atlas reads the first option anyway.

---

## Template 1 — TOCTOU / ordering race

**Use when:** two-step state change where observers can interleave (atomic swap + sidecar flag, read-modify-write on shared state, multi-handler cfg mutation).

```markdown
## Concern

In `<file:line>`, `<operation A>` happens before `<operation B>`. A `<observer — scheduler tick / concurrent handler / etc.>` can observe the state between A and B:

1. A: <what gets stored/mutated>
2. [concurrent observer fires here; sees post-A, pre-B state]
3. B: <what gets stored/mutated>

## Failure mode

<one concrete scenario — "operator picks X, scheduler tick runs, overwrites with Y, operator sees Y">

## Recommended fix

<one sentence>. Example code:

\`\`\`go
// before
opA()
opB()

// after
opB()  // sidecar / flag must precede the observable swap
opA()
\`\`\`

## References

- PR #<N>, commit `<sha>`
- `<file>:<line>`
- Related pattern: CAUGHT.md #1 (scheduler↔override race)
```

---

## Template 2 — silent-error (safety-critical path returns nil on failure)

**Use when:** a Restore, Close, panic-recover, or watchdog path swallows an error. Especially dangerous when the error indicates the operation did NOT happen.

```markdown
## Concern

`<function>` in `<file:line>` logs `<error condition>` but returns `nil` to the caller. This is a last-line-of-defence path — when it returns nil, the caller believes the operation succeeded.

\`\`\`go
// current
if <failure condition> {
    log.Warn(...)
    return nil  // ← claims success
}
\`\`\`

## Why it's safety-critical

<one sentence — why the caller needs to know; what the caller does differently on error>

## Failure mode

<one concrete scenario — "watchdog fires Restore, BMC busy, Restore logs and returns nil, fan stays at last commanded PWM">

## Recommended fix

Propagate the error. Caller decides policy (retry, alarm, escalate).

\`\`\`go
if <failure condition> {
    return fmt.Errorf("<op> failed: <detail>")
}
\`\`\`

## References

- PR #<N>, commit `<sha>`
- `<file>:<line>`
- Related rule: `.claude/rules/<rule-file>` — `<RULE-ID>` if applicable
- Related pattern: CAUGHT.md #5 (IPMI Restore silent-error)
```

---

## Template 3 — resource leak / missing cleanup

**Use when:** a goroutine spawns without a ctx.Done() exit, an os.Open has no paired Close, a sync.Pool Put is skipped on an error path, etc.

```markdown
## Concern

`<file:line>` <creates/spawns/opens> `<resource>` but no code path <closes/cancels/releases> it on `<error / panic / shutdown>`.

\`\`\`go
// current
<code showing the spawn/open without paired cleanup>
\`\`\`

## Failure mode

<concrete scenario — "daemon shutdown ctx cancels, but goroutine X has no <-ctx.Done() case, goroutine leaks until process exit; tests see a race detector warning">

## Recommended fix

<one sentence>. Example:

\`\`\`go
// after
<code with defer Close() or <-ctx.Done() branch>
\`\`\`

## References

- PR #<N>, commit `<sha>`
- `<file>:<line>`
```

---

## Template 4 — duplicate enumeration / registry shadow

**Use when:** a new backend delegates to another backend, or two backends match overlapping hardware. Check the Enumerate paths of both.

```markdown
## Concern

`<backend A>.Enumerate` at `<file:line>` and `<backend B>.Enumerate` at `<file:line>` both return channels for `<overlap source — hwmon chip, PCI device, etc.>` on `<trigger — this kind of hardware>`. The registry ends up with duplicate tagged entries.

\`\`\`go
// backend A (typically the delegating / specialised one)
<snippet>

// backend B (typically the general / hwmon-style one)
<snippet>
\`\`\`

## Failure mode

<concrete scenario — "every fan appears twice in /api/hardware; every controller tick writes the same PWM through two code paths; calibrate sweeps treat them as independent">

## Recommended fix

<one sentence>. Typical shape: the specialised backend claims the overlapping hardware by path, and the general backend's Enumerate filters out any path already claimed by a specialised backend. Coordination via a shared claim set in `internal/hal/`, not per-backend ad-hoc.

## References

- PR #<N>, commit `<sha>`
- `<file A>:<line>`, `<file B>:<line>`
- Related pattern: CAUGHT.md #9 (halhwmon/halasahi duplicate enumeration)
```

---

## Template 5 — cache-poisoning / fallback-locked

**Use when:** a cache is populated from a helper that silently returns a fallback value on error. The first-ever read can lock in the wrong value forever.

```markdown
## Concern

`<file:line>` caches the result of `<helper>` with `<cached-flag = true>`. `<helper>` silently returns `<fallback value>` on any error. The first-call path fires during `<triggering moment — daemon start, first tick, udev race window>` where the error condition is plausible.

\`\`\`go
// current
if !c.cached {
    c.value = <helper>()  // silently returns <fallback> on err
    c.cached = true
}
return c.value
\`\`\`

## Failure mode

<concrete scenario — "udev race at boot, helper errors, returns fallback, cached = true, daemon runs with fallback for its lifetime">

## Recommended fix

Distinguish fallback from real. Either (a) helper returns `(value, isReal bool)` and cache only sets cached=true on isReal, or (b) helper returns `(value, error)` and the caller retries on error without caching.

## References

- PR #<N>, commit `<sha>`
- `<file>:<line>`
- Related pattern: CAUGHT.md #3 (maxRPM cache locks 2000 RPM fallback)
```

---

## Template 6 — durability gap (atomic-rename without fsync)

**Use when:** `os.WriteFile(tmp, ...); os.Rename(tmp, dst)` without a `Sync()` between them.

```markdown
## Concern

`<file:line>` writes `<tmp path>` and renames it over `<dst path>` atomically, but does not call `f.Sync()` or `os.File.Sync()` on the tmp file before the rename. Filesystem guarantees the rename is atomic but does NOT guarantee the tmp file's contents are on disk at rename time.

\`\`\`go
// current
os.WriteFile(tmp, data, 0644)  // returns before flush
os.Rename(tmp, dst)
\`\`\`

## Failure mode

Power loss / crash between `WriteFile` returning and the OS flushing page cache → dst exists (rename replayed from journal) but is zero-length.

## Recommended fix

\`\`\`go
f, err := os.Create(tmp)
if err != nil { return err }
if _, err := f.Write(data); err != nil { f.Close(); return err }
if err := f.Sync(); err != nil { f.Close(); return err }
if err := f.Close(); err != nil { return err }
return os.Rename(tmp, dst)
\`\`\`

For extra paranoia (not usually needed), `Sync()` the parent directory too.

## References

- PR #<N>, commit `<sha>`
- `<file>:<line>`
- Related pattern: CAUGHT.md #7 (persistModule missing fsync)
```

---

## Template 7 — accept-loop stall (per-connection blocking work before goroutine spawn)

**Use when:** a listener does per-connection work — peek, handshake, auth — inline in the `Accept()` loop before handing off to a handler goroutine.

```markdown
## Concern

`<file:line>` accepts a connection and executes `<blocking operation>` inline before spawning the handler goroutine:

\`\`\`go
for {
    conn, err := listener.Accept()
    if err != nil { ... }
    <blocking op — io.ReadFull, TLS handshake, auth>  // ← blocks Accept loop
    go handle(conn)
}
\`\`\`

## Failure mode

Single-socket DoS. Attacker opens a TCP connection, sends nothing (or sends one byte then stalls). `<blocking op>` waits indefinitely. The `Accept()` loop stops running. No new legitimate connections accepted.

## Recommended fix

Move the per-connection work inside the spawned goroutine so each slow/silent client blocks only itself:

\`\`\`go
for {
    conn, err := listener.Accept()
    if err != nil { ... }
    go func(c net.Conn) {
        // set a short deadline first
        c.SetReadDeadline(time.Now().Add(handshakeTimeout))
        <blocking op>
        handle(c)
    }(conn)
}
\`\`\`

## References

- PR #<N>, commit `<sha>`
- `<file>:<line>`
- Related pattern: CAUGHT.md #8 (TLS sniffer Accept-loop stall)
```

---

## Template 8 — namespace collision (cross-namespace uniqueness not enforced)

**Use when:** two independent input namespaces feed into a single downstream keyspace (same map key, same hwmon path, same channel ID) but validate() only enforces uniqueness within each namespace.

```markdown
## Concern

`<downstream consumer — file:line>` keys state by a user-supplied `<name / id>` drawn from both `<namespace A>` and `<namespace B>`. `<validate file:line>` enforces uniqueness within each namespace but does not intersect them. A config with a <namespace A> item named "X" and a <namespace B> item named "X" passes validation, then collides downstream.

## Failure mode

<concrete scenario — "sensor named 'cpu' and fan named 'cpu' both passed to HistoryStore; sparkline shows interleaved values">

## Recommended fix

One-line addition to validate():

\`\`\`go
for name := range namespaceA {
    if _, collides := namespaceB[name]; collides {
        return fmt.Errorf("name %q used by both <A> and <B>; choose distinct names", name)
    }
}
\`\`\`

Alternative: namespace the downstream keyspace (`"sensor:cpu"` vs `"fan:cpu"`). Costs more churn and breaks existing dashboard URLs; not recommended.

## References

- PR #<N>, commit `<sha>`
- `<file>:<line>` (validate), `<file>:<line>` (downstream consumer)
- Related pattern: CAUGHT.md #2 (sensor/fan name collision)
```

---

## Usage notes

When filing an issue:

1. Pick the template that matches (use CAUGHT.md pattern library if unsure).
2. Fill in PR #, file:line, the one-sentence failure-mode, the one-sentence recommended fix.
3. Delete any template scaffolding you didn't need.
4. Target: issue body under 80 lines. If it's longer, either (a) there's genuinely more than one concern — file as two issues, or (b) the explanation can be tighter.

If the bug doesn't fit any template, file it freeform — but add a new template to this file once the class shows up a second time. Pattern library grows from real cases, not speculation.
