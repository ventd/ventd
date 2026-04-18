# bridge-phase1-mvp

**Care level: HIGH.** New infrastructure daemon handling GitHub webhooks
and pushing to Telegram. Phase 1 is read-path only (no Claude API calls,
no shell execution of AI-authored content) — this keeps the risk surface
narrow while proving the chat ergonomics.

You are Claude Code. You are building `PhoenixDnB/cowork-bridge` from
scratch. The repo was created empty with an auto-init README; replace it.

**Working directory:** clone `PhoenixDnB/cowork-bridge` to
`/home/cc-runner/cowork-bridge`. Do all work there. Open a single PR
against `main` with everything.

## Goal

A FastAPI daemon that:
1. Receives GitHub webhooks on `POST /webhook/github`.
2. Verifies `X-Hub-Signature-256` via HMAC-SHA256 on the raw body.
3. Deduplicates using `X-GitHub-Delivery` (SQLite WAL, `INSERT OR IGNORE`).
4. Formats events into readable Telegram messages.
5. Posts to the correct Telegram forum topic for role-labeled events
   (`role:atlas` → atlas topic; `role:cassidy` → cassidy topic;
   `role:mia` → mia topic; otherwise → general topic).
6. Exposes `GET /healthz` returning `{"status":"ok","db":"ok"}`.
7. Notifies systemd via `sd_notify` on startup.

No dispatch path. No Claude API calls. No shell-out on AI content. No
chat → GitHub write-path. Those are Phase 2.

## Stack (non-negotiable)

- Python 3.12 (`python3.12 -m venv .venv`)
- FastAPI + uvicorn (ASGI)
- `python-telegram-bot` v21+ (async API)
- SQLite via stdlib `sqlite3` with `PRAGMA journal_mode=WAL`
- Pydantic v2 for config
- `httpx` for outbound HTTP (sync, no need for async unless FastAPI
  request handler blocks)
- pytest + pytest-asyncio for tests
- ruff for lint + format
- No Django, no SQLAlchemy, no Celery, no Redis. This is one process,
  one SQLite file, one systemd unit.

## Repository layout

```
cowork-bridge/
├── README.md                 # setup runbook; BotFather + webhook steps
├── .gitignore
├── pyproject.toml            # project metadata + ruff config + deps
├── LICENSE                   # MIT
├── src/
│   └── cowork_bridge/
│       ├── __init__.py
│       ├── __main__.py       # python -m cowork_bridge entrypoint
│       ├── config.py         # pydantic Settings, loads from env
│       ├── main.py           # FastAPI app factory
│       ├── webhook.py        # GitHub webhook handler
│       ├── telegram.py       # Telegram client wrapper
│       ├── formatter.py      # pure function: event → message
│       ├── storage.py        # SQLite wrapper
│       └── events.py         # pydantic models for GitHub payloads
├── tests/
│   ├── conftest.py           # pytest fixtures
│   ├── test_hmac.py          # HMAC verification edge cases
│   ├── test_storage.py       # SQLite dedup, WAL mode
│   ├── test_formatter.py     # event → message snapshots
│   ├── test_webhook.py       # end-to-end via TestClient + mocked Telegram
│   └── data/
│       ├── push.json         # sample GitHub payloads
│       ├── issues_labeled.json
│       ├── pull_request_opened.json
│       └── pull_request_closed.json
├── deploy/
│   ├── cowork-bridge.service # systemd unit, LoadCredential= for secrets
│   ├── install.sh            # idempotent installer
│   └── credentials/
│       └── README.md         # explains the three .env files expected
└── .github/
    └── workflows/
        └── ci.yml            # ruff check + pytest on push/PR
```

## Key implementation decisions (do not rediscover these)

### HMAC verification

```python
import hmac, hashlib
def verify(raw_body: bytes, signature_header: str, secret: bytes) -> bool:
    if not signature_header.startswith("sha256="):
        return False
    expected = "sha256=" + hmac.new(secret, raw_body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, signature_header)
```

**Critical:** verify on the **raw body BEFORE** JSON parsing. FastAPI's
`request.body()` gives you raw bytes; do not use `request.json()` first.
Use `hmac.compare_digest`, never `==`.

### Idempotency

SQLite schema:

```sql
CREATE TABLE IF NOT EXISTS deliveries (
    delivery_id TEXT PRIMARY KEY,
    received_at TEXT NOT NULL,
    event_type  TEXT NOT NULL,
    action      TEXT,
    processed   INTEGER NOT NULL DEFAULT 0
);
```

On each webhook: `INSERT OR IGNORE INTO deliveries (...) VALUES (?, ?, ?, ?, 0)`.
If `cursor.rowcount == 0`, the delivery was already processed → return 200
without posting to Telegram. GitHub retries on non-2xx; the 2xx prevents
retries without creating duplicate chat messages.

### SQLite connection

One connection per request is fine (SQLite is fast; WAL makes concurrent
readers painless). Set once at startup:

```python
conn = sqlite3.connect(db_path, isolation_level=None, check_same_thread=False)
conn.execute("PRAGMA journal_mode=WAL")
conn.execute("PRAGMA synchronous=NORMAL")
conn.execute("PRAGMA foreign_keys=ON")
```

`isolation_level=None` = autocommit; you manage transactions explicitly
with `BEGIN`/`COMMIT` when doing multi-statement work.

### Topic routing

Given a webhook payload, extract the set of labels (for issues/PRs) and
map:

```python
ROLE_TOPIC = {
    "role:atlas":   ATLAS_TOPIC_ID,
    "role:cassidy": CASSIDY_TOPIC_ID,
    "role:mia":     MIA_TOPIC_ID,
}
def pick_topic(labels: list[str]) -> int:
    for label in labels:
        if label in ROLE_TOPIC:
            return ROLE_TOPIC[label]
    return GENERAL_TOPIC_ID
```

Events without labels (push, release, etc.) all go to `GENERAL_TOPIC_ID`.
If a PR/issue has multiple role labels, post to each matching topic (deal
with this via a `set` of topic IDs, dedup'd).

### Events to handle in Phase 1

Only these four. Everything else → log and ignore (not 404, just skip the
Telegram post and return 200):

1. `issues` with `action in {"opened", "labeled", "closed", "reopened"}`
2. `pull_request` with `action in {"opened", "closed", "ready_for_review", "reopened"}`
3. `pull_request_review` with `action == "submitted"` (ignore draft
   reviews: `review.state == "pending"` → skip)
4. `issue_comment` with `action == "created"` — only if the issue has a
   `role:*` label

For each, the Telegram message format is:

```
<b>{icon} {title}</b>
{repo}#{number} · {actor}
{one_line_excerpt_or_action_description}
🔗 {html_url}
```

Icons: 🐛 issues opened, 🏷️ labeled, ✅ closed, 🔄 reopened, 🔀 PR opened,
🟢 PR merged, 🔴 PR closed-without-merge, 💬 issue_comment, 👀 review.

Use `parse_mode="HTML"` in Telegram. Escape user-controlled content
(titles, bodies, logins) via `html.escape()`.

### Configuration (pydantic Settings)

```python
class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="BRIDGE_", env_file=None)

    github_webhook_secret: SecretStr
    telegram_bot_token: SecretStr
    telegram_chat_id: int           # supergroup chat_id (negative int)
    telegram_topic_atlas: int       # message_thread_id
    telegram_topic_cassidy: int
    telegram_topic_mia: int
    telegram_topic_general: int
    telegram_topic_ops: int         # for heartbeats + errors
    db_path: Path = Path("/var/lib/cowork-bridge/bridge.db")
    log_level: str = "INFO"
```

Secrets come from environment at runtime; systemd's `LoadCredential=` sets
`BRIDGE_GITHUB_WEBHOOK_SECRET` etc. from files mode 0640.

### systemd unit

`/etc/systemd/system/cowork-bridge.service`:

```ini
[Unit]
Description=Cowork Bridge — GitHub → Telegram
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
User=cowork-bridge
Group=cowork-bridge
WorkingDirectory=/opt/cowork-bridge
ExecStart=/opt/cowork-bridge/.venv/bin/python -m cowork_bridge
Restart=on-failure
RestartSec=5s

# Secrets — each file 0640 root:cowork-bridge
LoadCredential=github_webhook_secret:/etc/cowork-bridge/github.secret
LoadCredential=telegram_bot_token:/etc/cowork-bridge/telegram.token

# Env vars: the service reads $CREDENTIALS_DIRECTORY and fills
# BRIDGE_* from the files at startup (handle this in config.py's
# model_post_init or a pre-startup shim)
Environment=BRIDGE_TELEGRAM_CHAT_ID=...
Environment=BRIDGE_TELEGRAM_TOPIC_ATLAS=...
# etc — the non-secret IDs go here

# Sandboxing
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
StateDirectory=cowork-bridge
StateDirectoryMode=0750

[Install]
WantedBy=multi-user.target
```

The installer creates the user, directory, sets up the venv, copies unit
file, does NOT set secrets (user does that step manually per README).

### Heartbeat

Simple background task: every 1 hour, post `"🟢 bridge alive • {uptime}"`
to the `ops` topic. If the heartbeat stops, user knows the tunnel rotated
or the daemon died. Use `asyncio.create_task` on app startup; cancel on
shutdown.

### Logging

`structlog` is overkill. Use stdlib `logging` with JSON output:

```python
logging.basicConfig(
    level=settings.log_level,
    format='{"ts":"%(asctime)s","lvl":"%(levelname)s","logger":"%(name)s","msg":%(message)s}',
)
```

Log every webhook delivery with delivery_id + event + action. NEVER log
secrets, HMAC signatures, or full payload bodies (use `payload[:200]`
if you must log body fragments for debugging).

## Tests

Pytest, unittest-style assertions OK. Required coverage:

1. **`test_hmac.py`**
   - Valid signature → True
   - Invalid signature (wrong secret) → False
   - Missing `sha256=` prefix → False
   - Empty body → verifies correctly
   - Signature over modified body → False
   - Timing: `hmac.compare_digest` is used (inspect source, not behavior)

2. **`test_storage.py`**
   - First insert of delivery_id → rowcount == 1
   - Second insert of same delivery_id → rowcount == 0
   - Concurrent inserts (two threads) → exactly one succeeds
   - Database file is created with WAL mode (verify via PRAGMA query)

3. **`test_formatter.py`**
   - For each of the 4 sample payloads in `tests/data/`, snapshot the
     output message. Commit snapshots as `.txt` files next to the JSON.
     On mismatch, print diff and fail. Update via `UPDATE_SNAPSHOTS=1`
     env var.
   - HTML escaping: title with `<script>` is escaped, not raw.
   - Unicode in titles round-trips.

4. **`test_webhook.py`**
   - FastAPI TestClient, mock Telegram via monkeypatched send function.
   - Valid signed request + issues-opened payload → 200 + mock called
     once with correct topic ID.
   - Invalid signature → 401, mock NOT called.
   - Replay of same delivery_id → 200, mock called ONCE total.
   - Event type `push` (not in handled set) → 200, mock not called.
   - Event with `role:atlas` label + `role:cassidy` label → mock called
     twice (once per topic).
   - `GET /healthz` → 200 with `{"status":"ok","db":"ok"}`.

Target: >85% line coverage. Run `pytest --cov=src/cowork_bridge --cov-report=term-missing`
in CI; fail if coverage drops.

## CI workflow

`.github/workflows/ci.yml`:

```yaml
name: CI
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with: {python-version: '3.12'}
      - run: pip install -e '.[dev]'
      - run: ruff check .
      - run: ruff format --check .
      - run: pytest --cov=src/cowork_bridge --cov-report=term-missing --cov-fail-under=85
```

No Docker, no matrix, no deployment step. Single Python version.

## README.md runbook

Must cover, with exact commands:

1. Create Telegram bot via BotFather (`/newbot`), get token.
2. Create supergroup, upgrade to topics-enabled, add bot as admin.
3. Find `chat_id` — include a one-liner:
   `curl "https://api.telegram.org/bot<TOKEN>/getUpdates"` after sending
   a message in the group, parse `chat.id`.
4. Find `message_thread_id` for each topic — user sends a message in the
   topic, run `getUpdates` again, note `message_thread_id`.
5. Set up GitHub webhook on ventd/ventd:
   - URL: `https://<trycloudflare-host>/webhook/github`
   - Content type: `application/json`
   - Secret: generated via `openssl rand -hex 32`
   - Events: issues, pull_request, pull_request_review, issue_comment
6. Deploy:
   ```
   git clone https://github.com/PhoenixDnB/cowork-bridge.git /opt/cowork-bridge
   cd /opt/cowork-bridge
   sudo ./deploy/install.sh
   ```
7. Set secrets:
   ```
   echo -n '<webhook-secret>' | sudo tee /etc/cowork-bridge/github.secret
   echo -n '<bot-token>' | sudo tee /etc/cowork-bridge/telegram.token
   sudo chmod 0640 /etc/cowork-bridge/*
   sudo chown root:cowork-bridge /etc/cowork-bridge/*
   ```
8. Edit `/etc/cowork-bridge/env` with chat_id + topic IDs.
9. `sudo systemctl enable --now cowork-bridge`
10. Verify: tail `journalctl -u cowork-bridge -f`; label an issue;
    expect a Telegram post within 2s.

## Definition of done

- Repo populated per the layout above.
- `ruff check` and `ruff format --check` clean.
- `pytest --cov` passes at ≥85%.
- CI workflow green on the PR.
- README runbook is followable start-to-finish by someone who has
  never seen this repo.
- PR opened `main` ← `claude/phase1-mvp` as ready-for-review.
- PR description has STATUS / SUMMARY / CONCERNS / FOLLOWUPS sections.

## Out of scope (Phase 2, explicitly not in this PR)

- Any chat → GitHub dispatch.
- Any invocation of `claude -p`, spawn-mcp, or the Anthropic API.
- Token-bucket rate limiting.
- Circuit breaker.
- Cloudflare named tunnel setup (Phase 1 uses trycloudflare; note in
  README that this is temporary).
- Anthropic Workspace setup.
- Authentication for any non-webhook endpoints (healthz is public OK
  since it returns no secrets).
- E2EE, signed commands, per-user allowlists.

If you find yourself writing code that touches Claude API clients, shell
execution, or webhook→shell paths, STOP — that's Phase 2 and must not
land in this PR.

## Branch and PR

- Branch: `claude/phase1-mvp`
- Target: `main` (the default branch of PhoenixDnB/cowork-bridge)
- PR title: `feat: Phase 1 MVP — GitHub → Telegram bridge`
- Open as ready-for-review (NOT draft).

## Constraints

- CGO N/A (Python). But: no C extensions requiring compilation beyond
  what pip wheels provide.
- No database other than SQLite.
- No message queue.
- No Docker/Kubernetes/Terraform files.
- Python 3.12 minimum (match phoenix-desktop's spawn-mcp).
- All dependencies pinned in pyproject.toml with `~=` major-lock.

## Reporting

Standard Atlas reporting format. Additional sections:

- `FILES_CREATED` — full tree listing, file sizes.
- `COVERAGE` — pytest-cov output tail.
- `DEPLOY_DRYRUN` — paste output of `./deploy/install.sh --dry-run`.
- `SMOKE_TEST` — paste output of `python -m cowork_bridge` with all
  required env vars set to placeholder values, showing it comes up
  and logs "ready" before you kill it.

## Time budget

3 hours wall-clock. This is straightforward scaffolding; the design
decisions above eliminate ~70% of the "what do I use" thinking.

## Final note

Care level HIGH because this daemon will eventually process webhook
events with AI-spending implications (Phase 2). Phase 1 contains no
such risk, but getting the HMAC path, idempotency, and secret-handling
correct NOW sets the foundation for Phase 2 to layer on safely. Do
not cut corners on HMAC or SQLite WAL setup — those are the two pieces
most likely to be regretted if done wrong.
