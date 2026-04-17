# spawn-mcp

Minimal MCP server that spawns Claude Code sessions in detached tmux windows
on phoenix-desktop. Exposes a single tool, `spawn_cc(alias)`, that Cowork
(running in the Claude web/desktop app) can call to dispatch work without
human intervention.

Architecture:

    Cowork (web/desktop)
      |
      |  streamable-http over cloudflared tunnel (OAuth-gated)
      v
    spawn-mcp on phoenix-desktop  -- listens on 127.0.0.1:8891
      |
      |  tmux new-session -d -s cc-<alias> 'cd ~/src/ventd && claude <prompt>'
      v
    tmux server as user `phoenix`
      |
      v
    claude CLI reads .cowork/prompts/<alias>.md and starts the agent loop

Security model:

  1. Transport: streamable-http behind cloudflared named tunnel (reuses
     the same hostname pattern as github-mcp-server).
  2. Auth: OAuth via the existing `ventd-cowork` GitHub OAuth app.
     spawn-mcp validates the bearer token against GitHub and checks that
     the authenticated user is the repo owner (PhoenixDnB). No user ==
     no tool call.
  3. Command allowlist: spawn_cc only accepts aliases that exist as
     files at `.cowork/prompts/<alias>.md` on the repo's cowork/state
     branch, fetched over HTTPS at tool-invocation time. No arbitrary
     shell, no env passthrough, no file paths from arguments.
  4. Process isolation: every spawn runs as the `cc-runner` system user
     (not `phoenix`, not `root`). cc-runner has a bounded home, read-only
     access to /home/phoenix/src/ventd via bind-mount, and no sudo.
  5. Tmux sessions are named `cc-<alias>-<shortid>` so concurrent dispatches
     don't collide. Sessions auto-terminate when the claude CLI exits.

See INSTALL.md for the one-time deployment on phoenix-desktop.
