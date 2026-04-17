# How to start a Claude Code session

Quick reference. Keep this tab open.

## Activation message

Open a terminal on the Desktop (`phoenix@192.168.7.209`), `cd` into
any git working copy of `ventd/ventd`, run `claude`, then paste:

```
Read the prompt at .cowork/prompts/<TASK-ID>.md on the cowork/state branch of origin and execute it exactly as written. Do not deviate from the allowlist, model assignment, or reporting contract it specifies.
```

Replace `<TASK-ID>` with the task you want CC to run. Current
available names are listed in `.cowork/prompts/INDEX.md`.

## One-time Desktop setup

Creates six independent working copies so up to six CC instances
can run concurrently without stepping on each other:

```bash
cd ~
for i in 1 2 3 4 5 6; do
  [ -d "ventd-cc$i" ] || git clone git@github.com:ventd/ventd.git "ventd-cc$i"
done
```

Each directory is a totally independent checkout. CC opens its own
branch per task; two CC instances in different directories cannot
corrupt each other's state.

## Per-session workflow

1. Open a new terminal tab on the Desktop.
2. `cd ~/ventd-ccN` (pick any N that isn't already busy).
3. `claude`
4. Paste the activation message with the task ID.
5. Leave it running. CC will push a branch and open a draft PR when
   done; Cowork polls for the PR and takes over from there.

## Reusing a working copy

After a task finishes, the working copy is safe to reuse for a
different task — each CC run checks out its own branch from
`main`. You do not need to clean up between tasks.

If `main` has moved while you were away, a `git pull` on first use
catches the working copy up; CC will also do this as part of its
setup when it runs.

## What NOT to do

- Do not run two CC instances in the same `~/ventd-ccN` directory.
  Pick different numbers.
- Do not edit files in the working copy by hand while CC is active
  in it.
- Do not interrupt a running CC session mid-work; it may leave a
  half-committed branch. Let it finish, or explicitly ask it to
  push a `[BLOCKED]` draft PR if you need to stop.

## Hardware-in-the-loop safety

When CC is running on the Desktop (HIL rig) and the task involves
real `/sys/class/hwmon` access, CC may read freely but must never
write PWM values without the prompt explicitly authorising it.
The prompts are written to enforce this; if you see CC attempting
a PWM write it wasn't told to do, stop the session and let Cowork
know.

## Capacity

Desktop has 32 threads / 32 GB. Up to ~8 concurrent CC instances
are comfortable. Start with 3–4 during this phase while the
pipeline is small; scale up when Phase 2 fans out across backends.

## Fallbacks

- If the Desktop is offline: the original dev VM (VMID 950) works
  too; same activation message, just mosh in first.
- If Claude Code is not installed locally: follow Anthropic's
  install guide; it's a single binary.

## Trouble?

Tell Cowork. Escalations happen in the chat; Cowork logs them and
unblocks where possible.
