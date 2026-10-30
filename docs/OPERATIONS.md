# Local operations

WebhookLab runs a synthetic local webhook workflow. It has a static web frontend, an API, a worker, a demo receiver, and PostgreSQL. The receiver endpoints are fixed local services; the application does not send to arbitrary external URLs.

## First start on Windows

Install Docker Desktop with Compose and start its Linux container engine. From PowerShell in this repository:

```powershell
.\scripts\Init.ps1
.\scripts\Start.ps1
```

Init generates private configuration and `.local/LOCAL-CREDENTIALS.txt`. Git ignores these files. Read the local password there, then open exactly `http://127.0.0.1:8097` and sign in as `operator@webhooklab.test`. The login form never prefills its password. Existing account hashes and event data are not reset on restart.

Start builds the shared Go image and static Next.js web image sequentially, then starts the five Compose services with health checks. The first build needs network access for images and dependencies. The Go runtime serves the API and receiver; Nginx serves the static export and proxies `/api`.

## Normal lifecycle

```powershell
# Build current source and start.
.\scripts\Start.ps1

# Start existing images without rebuilding changed source.
.\scripts\Start.ps1 -NoBuild

# Stop containers and retain the persistent database volume.
.\scripts\Stop.ps1
```

Use `docker compose ps` and `docker compose logs` for local diagnostics. Logs should not contain passwords, cookies, CSRF tokens, signing secrets, or request bodies. Review logs before sharing them. Stopping the stack does not erase history; deleting a database volume is a separate destructive action, not a restart procedure.

## Addresses and configuration

| Setting | Local value or responsibility |
| --- | --- |
| Browser origin | `http://127.0.0.1:8097` |
| Application PostgreSQL | Loopback port 5448 |
| Isolated test PostgreSQL | Loopback port 5449 |
| API listener | Internal port 8080 |
| Receiver listener | Internal port 8082 |
| `WEBHOOK_DB_URL` | Server-side PostgreSQL connection |
| `WEBHOOK_APP_ORIGIN` | Exact allowed browser origin |
| `WEBHOOK_DEMO_PASSWORD` | Required generated password for an empty user table |
| `WEBHOOK_COOKIE_SECURE` | Secure cookie flag, false for local HTTP |
| `WEBHOOK_SIGNING_SECRET` | Server-only shared master secret for worker and receiver |
| `WEBHOOK_RECEIVER_BASE_URL` | Trusted receiver host, default `http://receiver:8082` |
| `WEBHOOK_API_ADDR`, `WEBHOOK_RECEIVER_ADDR` | Go listener overrides, defaults `:8080` and `:8082` |

`localhost` is a different browser origin from `127.0.0.1`. Keep the configured origin and browser address identical. Native test commands use the isolated database rather than the persistent application database.

Changing the seed password environment value does not rotate an existing user's password. Do not replace the signing secret during in-flight work without understanding the worker/receiver configuration boundary; both services need the same master secret. User inputs cannot select a destination host or inspect this secret.

Login throttling keys the immediate remote peer seen by the API. In the supplied reverse-proxy deployment, browser callers share Nginx's throttle scope. The lab does not provide per-organization or per-person rate-limit isolation.

## Demo controls and expected observations

| Receiver mode | Expected behavior |
| --- | --- |
| Healthy (`HEALTHY`) | Commit or deduplicate a receipt, then respond immediately |
| Return 503 (`FAIL`) | Return 503 without applying a synthetic effect |
| Apply, then delay (`TIMEOUT`) | Commit or deduplicate a receipt, then delay the response for 3.5 seconds; worker times out after 2 seconds |

Mode changes affect subsequent requests to that destination across the workspace. An in-flight handler may already have selected its behavior. Use a new synthetic event for a distinct scenario and inspect the stored attempts rather than inferring their result from the current receiver mode.

Queue pause stops new claims. Already claimed requests may finish, expired leases recover, and heartbeat continues. The UI's recent-worker indicator means a worker was seen within 15 seconds; it does not guarantee current request progress.

A cycle permits three attempts, with retries due after 2 seconds and 5 seconds. A dead-letter event can be redriven twice, for at most nine attempts across three cycles. The finite budget can end in `DEAD_LETTER` without successful delivery. Redrive keeps previous attempts and receipts. A timeout or abandoned attempt has an unknown remote outcome; a receipt is separate local receiver evidence. Do not create a fresh event merely to retry an uncertain create response: retain the original idempotency key and inputs.

## Verification

```powershell
.\scripts\Test.ps1
```

The native checks start temporary PostgreSQL on 5449, set `WEBHOOK_TEST_DB_URL`, run Go tests, then frontend unit checks and a static build. The script cleans up test resources rather than touching the persistent application database. `scripts/Go.ps1` supplies a pinned Go 1.27.3 toolchain with checksum verification, checked extraction paths, a completed-install marker, and local module/build caches. It restores process environment settings when it exits. Frontend checks use Node.js 22 and npm.

To add browser checks, first start the application and use the configured local browser:

```powershell
.\scripts\Test.ps1 -WithBrowser
```

Browser checks operate on the running stack at 8097 and add synthetic events to that persistent training workspace. They are distinct from the isolated backend test database. Playwright uses Edge locally and Chromium in CI; `WEBHOOK_BROWSER_CHANNEL` can select an installed channel. Request retries, receiver modes, and queue controls can affect other demo work, so avoid running a manual scenario concurrently with those checks.

Read [VERIFICATION.md](VERIFICATION.md) for commands actually executed, observations, and limitations. A test that was authored or skipped is not evidence of a passing execution.

## Probe an actual worker interruption

With the main Docker stack running and Node.js available:

```powershell
.\scripts\Recovery-Probe.ps1
```

This probe operates only the local WebhookLab stack on port 8097 and requires exactly one running worker belonging to this checkout. It discovers the selected local Docker named pipe or Unix socket, checks project/service labels and the working-directory label, and refuses a remote TCP Docker context. It temporarily changes the warehouse mode and queue control, creates one synthetic event, waits for a receiver effect while the attempt is still STARTED/SENDING, and sends SIGKILL through the local Docker Engine API. Avoiding a fresh CLI process for that signal helps preserve the two-second observation window. Restart still uses Compose.

After restart, the probe checks expired-lease recovery while paused, resumes claims, and checks that a later attempt succeeds without a second receiver effect. It marks the worker as needing restart before attempting the kill so cleanup can try to recover even if that operation fails partway through.

The probe asserts the resulting attempt/receipt counts rather than treating a restart as proof of recovery. After validating the event UUID, it also runs `psql` inside the database container to count `receiver_effects` rows for that event and the warehouse destination. It requires exactly one row, independently of the API receipt projection. Its cleanup restarts a killed worker if necessary and restores the original warehouse mode and pause setting. Do not run it alongside browser tests or a manual delivery scenario because those controls are shared. The synthetic event remains in the persistent workspace.

The local report is `.local/recovery-probe.json`; Git ignores it. A successful run records ABANDONED followed by SUCCEEDED, two attempts, two receipts, and one effect for that probe event, with `databaseEffectRows: 1` from the SQL check. Those are the script's checked conditions; consult [VERIFICATION.md](VERIFICATION.md) for observed execution evidence from the delivered version.

## Static web build and CSP

`frontend/Dockerfile` builds the Next.js export, then runs `generate-csp.mjs`. The helper hashes the actual inline bootstrap scripts in exported HTML and generates the Nginx script policy for that build. Rebuild the web image after changing exported source; copying a new HTML export without regenerating its header policy can block bootstrap scripts.

Nginx runs as the `nginx` user on port 8080, with PID and temporary paths under `/tmp` in the read-only container setup. The policy restricts script sources to this origin plus generated hashes; it permits inline styles for the static export. Do not describe this as a blanket ban on every inline asset. The browser receives the web assets and API through one origin.

## Troubleshooting and recovery boundaries

| Symptom | Check |
| --- | --- |
| Compose cannot start | Ensure Docker Desktop is running and the selected ports are free |
| Login or a mutation returns 403 | Use the exact 127.0.0.1 origin and refresh the session's CSRF token |
| Login is throttled | Wait for the recorded throttle window; the local reverse-proxy scope is shared |
| Create response is uncertain | Retry identical destination/type/payload with the same key and initiating account |
| Mutation returns 409 | Explicitly refresh and inspect the current version/state before deciding again |
| A delivery waits while queue is paused | Resume to permit new claims; in-flight work and lease recovery follow separate rules |
| A worker disappears during a request | Inspect the 10-second lease and later ABANDONED/recovery history; remote outcome may already include an effect |
| Receiver has one effect but event is dead letter | Timeout/crash can hide a committed remote effect; switch to Healthy and use an allowed redrive |
| Redrive is refused | Confirm DEAD_LETTER state, current version, note length, and remaining two-redrive budget |
| A new event is refused at capacity | Workspace capacity is 500 events; the application provides no deletion route |
| Test database port 5449 is busy | Stop the conflicting isolated test process; do not repoint tests at the application database |

The local workflow preserves database state across ordinary stop/start, but this repository does not supply production backups, restore drills, multi-organization access, arbitrary receiver hosting, or external secret rotation. Exported screenshots and test reports do not replace a database backup.
