# Verification record

Verified on **30 October 2026** using Windows, Docker Desktop's Linux engine, PostgreSQL 17, portable Go 1.27.3, Node 22.14.0 and Edge through Playwright. The recorded application is the local laboratory described in the README.

## Executed checks

| Check | Observed result |
| --- | --- |
| `scripts/Start.ps1` | Built the shared three-binary Go image, built the Next.js/Nginx image, and started all five services healthy. |
| `scripts/Test.ps1` | Passed all 12 named Go tests, all 13 frontend unit tests, the Next production export and CSP generation; removed its isolated test database afterward. |
| Native `go build ./cmd/...` and `go vet ./...` through `scripts/Go.ps1` | Passed; all three service entry points compiled. |
| `npm ci --ignore-scripts` | Passed on Windows and in the Linux frontend build; each installation reported zero known vulnerabilities at that time. Platform-specific optional package counts differed. |
| `npm run test:e2e`, actual application | All 5 Playwright workflows passed, with no skipped workflow. |
| `scripts/Recovery-Probe.ps1` | Killed the actual owned worker after a receiver effect committed, recovered its lease while paused, retried and verified one actual database effect row. |
| UI delivery gate | [PASS](UX-REVIEW.md): control/keyboard review, rendered text contrast and 30 responsive layouts. |
| PowerShell parsing and JavaScript helper syntax | Passed. |

The test wrapper was also run with a deliberately different inherited `WEBHOOK_TEST_DB_PASSWORD`. It used the private test configuration consistently and restored the inherited environment afterward. Test PostgreSQL ran on isolated loopback port 5449 with tmpfs storage; it did not clear the application's persistent volume on port 5448.

The frontend produced a static export, not an SSR server. The CSP generator found three distinct inline Next bootstrap scripts and generated their actual hashes. The Docker/Nginx build served that export successfully to the browser workflows. `script-src` uses self plus those hashes; `style-src` permits inline styles. This record does not call that policy an inline-style-free policy.

## What the tests established

The **7 PostgreSQL integration tests** exercised actual database transactions and HTTP handlers, including:

- atomic event/delivery/idempotency/audit persistence, including an injected delivery-insert failure after the event insert;
- concurrent same-key creation, changed-body conflicts and replay at the 500-event capacity;
- concurrent claims, pause behavior, expired-lease recovery and stale-completion fencing after a blocked row lock has been acquired;
- finite retry/redrive budgets, concurrent redrive conflicts and immutable history;
- concurrent receiver deduplication backed by a real `receiver_effects` row, signature validation and rejected invalid requests;
- receiver processing before a sender timeout, then successful duplicate processing without a second effect;
- sessions, strict JSON/path/version boundaries, exhausted-budget recovery and literal search behavior.

The **5 Go unit tests** covered exact numeric payload preservation, signature binding, strict JSON decoding, transport/result classification, bounded previews, redirects and untrusted target components. A known 2xx remains a successful acknowledgement even when preview-body reading later fails. The native unknown-outcome integration case injects the transport timeout boundary; the browser and process probe below add real local-service timing/process evidence.

The **13 frontend unit tests** covered validation, UTF-8 and Unicode limits, pagination, redrive eligibility, attempt explanations, CSRF/error transport and safe request-identity retention after uncertain creation.

The **5 browser workflows** used the real Go API, worker, receiver and PostgreSQL through Nginx:

1. Pause, record a queued event, resume and deliver it, while preserving the raw JSON number `9007199254740993.25`.
2. Delay the receiver beyond the worker's actual deadline, exhaust three timeout attempts, then redrive successfully with additional receipts and one effect.
3. Return real HTTP 503 responses through three bounded cycles: nine attempts, two redrives and no receiver effect.
4. Let a real creation request commit, discard its browser response, expire the session and retry after login using the same UUID. The replay returned HTTP 200 instead of creating another event.
5. Use keyboard login and a narrow viewport, conflict a stale receiver version, explicitly refresh, correct invalid JSON, filter records, inspect audit and sign out.

## Actual killed-worker result

At `2026-10-30T13:32:25.026Z`, the probe recorded event `5dd15a70-02f1-4522-8bce-41d12626c757` with:

```json
{
  "workerKilledAfterReceiverEffect": true,
  "leaseRecoveredWhilePaused": true,
  "finalState": "DELIVERED",
  "attemptStatuses": ["ABANDONED", "SUCCEEDED"],
  "totalAttempts": 2,
  "receiptCount": 2,
  "effectCount": 1,
  "databaseEffectRows": 1
}
```

The probe used the local Docker Engine API to send SIGKILL to the single running worker only after validating the Compose project and checkout ownership. It restarted that worker with Compose, then queried PostgreSQL independently with `psql` to count the corresponding effect row. It restored the original queue and receiver configuration.

The [detail screenshot](screenshots/03-event-detail.png) shows that same event. Its abandoned attempt remains unknown to the sender even after the receiver record is visible. For an abandoned attempt, duration ends at lease recovery; it is not a received HTTP response time and can exceed the two-second request timeout.

## Captures and boundaries

The four committed PNGs were captured from the running application, not drawn as mockups. The register capture deliberately paused new claims and created one synthetic queued event. UI checks additionally used clearly synthetic records for pagination and a deliberately failed browser GET for recovery feedback.

Measured routes were Deliveries, Send event, event detail, Demo receivers and Audit at widths 320, 390, 600, 768, 1024 and 1440 px. All 30 measured documents fit the viewport. Wide tables retained their own scrolling region. The capture and control-review runs recorded no JavaScript page error.

This is a finite local lab: two configured demo destinations, one operator, at most 500 events, three attempts per cycle and at most two redrives. Repeated attempts do not guarantee eventual successful delivery. The local receiver's one-effect result is backed by its own database transaction; it does not establish exactly-once delivery or the business outcome of another system.

The committed [CI workflow](../.github/workflows/verify.yml) repeats the checks on Linux, adds Go's race detector, runs the browser workflows and the killed-worker probe. Current public run results are available in [GitHub Actions](https://github.com/zalamanga/webhooklab/actions/workflows/verify.yml).

After local verification, the application services and isolated test services were stopped. The application volume `webhooklab_postgres_data` was preserved. Start it again with `scripts/Start.ps1` and use the private credentials generated in your checkout.
