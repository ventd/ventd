# Bridge Phase 1 — Tonight's Resume Checklist

**State as of 2026-04-18 ~10:05 UTC:**

- ✅ `PhoenixDnB/cowork-bridge` repo created (empty, auto-init README only)
- ✅ CC prompt staged: `.cowork/prompts/bridge-phase1-mvp.md` on `cowork/state`
- ✅ Memory #19 updated (ultrareview → Cassidy's lane)
- ❌ CC session `cc-bridge-phase1-mvp-feeea4` dispatched then killed — no PR opened, no code written
- ❌ Telegram bot not yet created
- ❌ Supergroup not yet created
- ❌ Webhook not yet registered on ventd/ventd

**Phase 1 scope locked:** GitHub → Telegram read-path only. No dispatch, no
Claude API, no shell execution of untrusted content. Phase 2 (dispatch)
deferred until Phase 1 runs in prod 24h.

## Tonight's 3-stage resume plan

### Stage A — Human pre-dispatch (15 min, can do anywhere)

1. **Create the Telegram bot.**
   - Open Telegram, message `@BotFather`, send `/newbot`.
   - Name: `Cowork Bridge` (display name, can change later).
   - Username: must end in `_bot`. Suggested: `phoenix_cowork_bot` or
     `ventd_cowork_bot` (whichever is free).
   - Save the token (format `8123456789:AAHxxxxxx...`). This is the
     `BRIDGE_TELEGRAM_BOT_TOKEN` secret.

2. **Create the Telegram supergroup with topics.**
   - Telegram → new group. Name it `ventd-cowork` (or similar).
   - Settings → convert to supergroup (if not automatic).
   - Settings → Topics → enable.
   - Create topics in this order: `atlas`, `cassidy`, `mia`, `general`,
     `ops`.
   - Add the `@phoenix_cowork_bot` you just created as an admin with:
     post-messages, pin-messages, manage-topics permissions.

3. **Capture the IDs.**
   - Send any message in the group and each topic (just type "x" five
     times, one in each topic).
   - Run on phoenix-desktop or your laptop:
     ```
     TOKEN='8123456789:AAHxxxxxx'
     curl -s "https://api.telegram.org/bot${TOKEN}/getUpdates" | jq .
     ```
   - From the JSON output, record:
     - `chat.id` (supergroup chat_id, negative integer starting with -100)
     - `message_thread_id` for each of the 5 topics (integer)
   - Save these in a scratch file; you'll paste them into the bridge
     config post-install.

4. **Generate the GitHub webhook secret.**
   ```
   openssl rand -hex 32
   ```
   Save the output. This is `BRIDGE_GITHUB_WEBHOOK_SECRET`.

### Stage B — CC dispatch + merge (CC does ~3h, human active ~10 min)

1. **Dispatch CC from Atlas.** The prompt is already staged; just say
   "Atlas, redispatch bridge-phase1-mvp" and Atlas will:
   - Verify `cc-bridge-phase1-mvp-*` not running.
   - Call `spawn_cc("bridge-phase1-mvp")`.
   - Poll GitHub for the PR against `PhoenixDnB/cowork-bridge:main`.

2. **Review + merge** — Atlas reviews against R1-R23 adapted for Python
   (HMAC correctness + SQLite WAL + pytest coverage ≥85% are the key
   gates; no rule-file binding applies since this isn't a ventd-internal
   invariant).

3. **If CC hits auth failure** (possible — phoenix-desktop's cc-runner
   may not have push auth to `PhoenixDnB/*`):
   - Atlas will see a `git push` error in the session log.
   - Recovery: the user grants cc-runner a PAT for personal repos OR
     CC pushes to a fork and opens PR cross-account. Will decide when
     error surfaces; not worth pre-solving.

### Stage C — Deploy on phoenix-desktop (20 min, human)

1. **Clone and install.**
   ```
   sudo mkdir -p /opt/cowork-bridge
   sudo chown $USER:$USER /opt/cowork-bridge
   git clone https://github.com/PhoenixDnB/cowork-bridge.git /opt/cowork-bridge
   cd /opt/cowork-bridge
   sudo ./deploy/install.sh
   ```
   The installer creates the `cowork-bridge` system user, sets up the
   venv, copies the systemd unit. It does NOT touch secrets.

2. **Write secrets.** Two files, mode 0640, owner `root:cowork-bridge`:
   ```
   sudo mkdir -p /etc/cowork-bridge
   echo -n '<webhook-secret-from-step-A4>' | sudo tee /etc/cowork-bridge/github.secret
   echo -n '<bot-token-from-step-A1>'      | sudo tee /etc/cowork-bridge/telegram.token
   sudo chmod 0640 /etc/cowork-bridge/*.secret /etc/cowork-bridge/*.token
   sudo chown root:cowork-bridge /etc/cowork-bridge/*
   ```

3. **Write non-secret config.** `/etc/cowork-bridge/env` with the IDs
   from step A3:
   ```
   BRIDGE_TELEGRAM_CHAT_ID=-1001234567890
   BRIDGE_TELEGRAM_TOPIC_ATLAS=2
   BRIDGE_TELEGRAM_TOPIC_CASSIDY=3
   BRIDGE_TELEGRAM_TOPIC_MIA=4
   BRIDGE_TELEGRAM_TOPIC_GENERAL=5
   BRIDGE_TELEGRAM_TOPIC_OPS=6
   ```
   (Actual topic IDs from `getUpdates`. Topic 1 is usually `General` —
   the default — which can be the `general` topic if you don't create a
   separate one.)

4. **Start the daemon.**
   ```
   sudo systemctl daemon-reload
   sudo systemctl enable --now cowork-bridge
   sudo systemctl status cowork-bridge
   sudo journalctl -u cowork-bridge -f
   ```
   Look for the startup log line that says "bridge ready on :8787" or
   similar. The heartbeat should post to the `ops` topic within ~5s.

5. **Expose via trycloudflare.**
   ```
   cloudflared tunnel --url http://127.0.0.1:8787
   ```
   Note the `trycloudflare.com` hostname it prints. Daemonize this via
   a second systemd unit if it works (CC's deploy/ should include an
   example unit — if not, file a follow-up).

6. **Register the GitHub webhook.** On `ventd/ventd`:
   - Settings → Webhooks → Add webhook
   - Payload URL: `https://<trycloudflare-hostname>/webhook/github`
   - Content type: `application/json`
   - Secret: the one from step A4
   - SSL verification: enabled
   - Events: check **Issues**, **Pull requests**, **Pull request
     reviews**, **Issue comments** (uncheck "Send me everything")
   - Active: ✅
   - Save.
   - Click into the webhook, go to "Recent Deliveries" — GitHub
     auto-sends a `ping` event on save. Should show green checkmark
     within 5s.

7. **Smoke test.**
   - Label any existing issue on ventd/ventd with `role:atlas`.
   - Expect: Telegram notification in the `atlas` topic within 2s.
   - If nothing appears: `journalctl -u cowork-bridge -n 50` for clues.

## Known risks tonight

- **trycloudflare URL rotates on cloudflared restart.** After any
  phoenix-desktop reboot or cloudflared service bounce, the URL changes
  and the GitHub webhook silently fails until re-registered. Mitigation
  for Phase 1: live with it; daily ops-topic heartbeat tells you if
  delivery is broken. Phase 2 will migrate to named tunnel ($10/yr +
  domain).

- **cc-runner auth to PhoenixDnB/* personal repos is untested.** If
  push fails, the two fallback paths are documented in Stage B step 3.

- **Phase 2 is NOT to be started** even if Phase 1 finishes early
  tonight. Observing Phase 1 for 24h in prod is the gate. No exceptions.

## Cost tonight

$0. No new subscriptions, no API keys, no paid tunnels. All existing
infrastructure reused.

## Follow-up issues to file (at Phase 1 merge time, not before)

1. "Migrate bridge from trycloudflare to named Cloudflare tunnel" —
   Phase 2 prereq. Requires domain.
2. "Create Anthropic Workspace `bridge-prod` with $50 spend cap" —
   Phase 2 prereq.
3. "Add daily digest message to bridge ops topic" — UX polish,
   not blocking.
4. "Extend `cc-runner` GitHub auth to PhoenixDnB/* if not already" —
   only if Stage B step 3 failed.

## State of the ventd queue tonight

Before returning to the bridge:
- Wave 1 closed. #285 merged.
- PR #299 (fix-290 regresslint) open, CI running, probably mergeable.
- CC sessions running: `cc-docs-cassidy-owns-ultrareview-ba67a7`,
  `cc-fix-287-watchdog-restoreone-binding-a47341`.
- Session merge count: 9. Next merge (fix-290) → 10 → ultrareview-2
  trigger issue to file for Cassidy.
- Phase 4 prompts staged (P4-PI-01, P4-HYST-01, P4-DITHER-01) ready
  to dispatch after docs-cassidy-owns PR merges.
- Phase 6 + 8 prompts also staged.

Atlas's plan after this commit: resume merging ventd PRs as they
land, file the ultrareview-2 trigger issue when fix-290 lands (count
=10) assuming the Cassidy ownership PR has also merged by then.
