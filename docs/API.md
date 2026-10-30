# HTTP API

Use the application origin `http://127.0.0.1:8097`. The API is served under `/api`; `localhost` is a different origin. All API-server paths require a session except exact `GET /api/health` and `POST /api/auth/login`. Public receiver hooks are internal services with HMAC authentication, not browser management endpoints.

## Conventions

- JSON errors use `{code,message}` with safe text messages.
- IDs and idempotency keys use canonical UUID strings. Timestamps are RFC3339 UTC.
- JSON request bodies are at most 32 KiB. Wrong types, unknown outer fields, trailing JSON, and fractional versions are rejected.
- Mutation `expectedVersion` values are nonnegative integers. Notes count Unicode code points after trimming.
- Lists have fixed page size 25, zero-based pages, and stable ordering.
- Payloads and response previews are text in the UI. Do not interpret them as HTML or parse arbitrary payload numbers through JavaScript floating point.

Pages use this shape:

```json
{"items":[],"page":0,"pageSize":25,"total":0}
```

## Session and health

| Route | Request | Successful response |
| --- | --- | --- |
| `GET /api/health` | None | `200 {"status":"ok"}` after a database check |
| `POST /api/auth/login` | `{email,password}` | `200 Session` and session cookie |
| `GET /api/auth/session` | Cookie | `200 Session` |
| `POST /api/auth/logout` | Cookie, Origin, CSRF | `204`, session revoked and cookie expired |

`User` is `{id,email,name}`. `Session` is `{user,csrfToken}`. The cookie is `webhooklab_session`, HttpOnly, SameSite=Lax, Path `/`, with a 12-hour expiry. The database stores a hash of the opaque session token. The local HTTP deployment uses `Secure=false`; `WEBHOOK_COOKIE_SECURE` controls it.

Login requires JSON and the exact configured `Origin`. Authenticated JSON mutations require that origin and `X-CSRF-Token` from the session response. Do not put passwords, cookies, or CSRF tokens in URLs, logs, or committed examples. Login failures are throttled by immediate remote address; browser users behind the supplied reverse proxy share that scope.

## Dashboard and controls

| Route | Request | Successful response |
| --- | --- | --- |
| `GET /api/dashboard` | None | `Dashboard` |
| `GET /api/destinations` | None | `Destination[]` |
| `PUT /api/queue` | `{expectedVersion,paused}` | Updated `QueueControl` |
| `PUT /api/destinations/{id}` | `{expectedVersion,mode}` | Updated `Destination` |

`QueueControl` is `{paused,version}`. Pause stops new claims; an already claimed request may finish. Expired leases recover while paused.

`WorkerStatus` is `{activeCount,lastSeenAt,staleAfterSeconds:15}`. Last-seen time may be null. Workers heartbeat every three seconds, including while paused. Recent observation is not a guarantee of uninterrupted health.

`Dashboard` is `{total,counts,queue,workers,serverTime}`. Counts have keys `QUEUED`, `SENDING`, `RETRY_WAIT`, `DELIVERED`, and `DEAD_LETTER`, derived from current stored deliveries.

`Destination` is `{id,name,route,mode,version}`. Fixed IDs are `warehouse` and `billing`, named Warehouse demo and Billing demo. Returned routes are relative. Modes are `HEALTHY`, `FAIL`, or `TIMEOUT`; both destinations initially use `HEALTHY`. There is no destination URL editor.

## Create and inspect events

| Route | Request | Successful response |
| --- | --- | --- |
| `GET /api/events?page=0&state=ALL&search=` | Query parameters | `Page<Delivery>` |
| `POST /api/events` | Create JSON and `Idempotency-Key` | `201 Delivery`, or `200` committed replay |
| `GET /api/events/{id}` | None | `DeliveryDetail` |
| `POST /api/events/{id}/redrive` | `{expectedVersion,note}` | Updated `Delivery` |

Create request example, with a new UUID in the `Idempotency-Key` header:

```json
{
  "destinationId": "warehouse",
  "eventType": "order.created",
  "payload": "{\"orderId\":\"DEMO-1001\",\"source\":\"synthetic\"}"
}
```

`eventType` accepts `order.created` or `order.cancelled`. `payload` is a nonempty string containing a UTF-8 JSON object, at most 16 KiB. The server compacts JSON without converting arbitrary numbers to float64 and stores the immutable envelope bytes. The API continues to represent payload as a JSON text string. Event type names do not trigger real order or payment processing.

The actor-scoped idempotency key is mandatory. Same key plus the same destination, type, and compact payload returns the same current delivery. Changed content with that key returns `409`. The workspace accepts at most 500 events, but committed identical replays remain available at capacity. An uncertain response should be retried with unchanged inputs and the same key.

Filters accept `state=ALL` or an exact delivery state. Search is a trimmed literal substring of ID or event type, at most 64 characters; SQL wildcard characters are escaped. Results sort by creation time descending, then ID descending.

`Delivery` contains:

```text
id, eventType, destinationId, destinationName, payload,
state, version, cycle, cycleAttempts, totalAttempts, redrives,
nextAttemptAt, createdAt, updatedAt
```

`id` is the stable event and delivery UUID. There is no separate delivery ID. Cycle starts at 1; `nextAttemptAt` is nullable. States are `QUEUED`, `SENDING`, `RETRY_WAIT`, `DELIVERED`, and `DEAD_LETTER`.

`DeliveryDetail` is `{delivery,attempts,receipt,audit}`. Receipt may be null. Attempts and event-specific history are bounded by the event's retry/redrive policy.

## Attempts, recovery, and redrive

Each cycle permits three attempts. Retry delays are 2 seconds and 5 seconds; request timeout is 2 seconds, and lease duration is 10 seconds. Any known HTTP 2xx succeeds, including when its best-effort response preview cannot be read completely. Other responses, timeout, or network failure consume the cycle budget. Expired `STARTED` attempts become `ABANDONED`, with remote outcome unknown. Repeated attempts do not guarantee eventual success: exhausted cycles end in `DEAD_LETTER`, with at most two explicit redrives available.

`Attempt` contains:

```text
id, number, cycle, status, startedAt, finishedAt,
httpStatus, durationMs, responsePreview, errorCategory, workerId
```

Status is `STARTED`, `SUCCEEDED`, `HTTP_ERROR`, `TIMEOUT`, `NETWORK_ERROR`, or `ABANDONED`. Completion fields may be null while started. A TIMEOUT or NETWORK_ERROR can still have a recorded non-2xx HTTP status when headers arrived but reading the response preview failed. Response preview is at most 2,048 bytes and is display text. Error categories are fixed safe labels rather than raw network errors. Attempt numbers keep growing across cycles.

For `ABANDONED`, `finishedAt` is the lease-recovery time and `durationMs` is elapsed database time from attempt start to recovery. It is not a measured HTTP response duration and can exceed the two-second request timeout.

Only `DEAD_LETTER` can be redriven. Supply the current expected version and a note of 10 to 500 Unicode code points after trimming. At most two redrives start two additional cycles, for at most nine total attempts. Redrive preserves every earlier attempt and receiver receipt. Stale or competing redrives return `409` without duplicate transition or audit events.

## Receiver evidence and signing

The Go receiver exposes database-backed `GET /health` and signed `POST /hooks/warehouse` and `POST /hooks/billing`. Its management controls belong to the authenticated API above.

Headers are `Webhook-Id`, `Webhook-Timestamp` as Unix seconds, and `Webhook-Signature` as `v1=<hexHMACSHA256>`. Derive a destination key with HMAC-SHA256(master secret, destination ID), using raw derived bytes. The envelope signing input is timestamp + `.` + event ID + `.` + exact envelope bytes. The receiver verifies IDs, size, a 300-second timestamp tolerance, and the signature using constant-time comparison before persistence.

The first receipt creates one synthetic effect. Identical duplicate requests increment the receipt count. The same destination/event ID with different body SHA256 returns `409`. A successful receiver response is `{received:true,eventId,duplicate}`.

`Receipt` is `{eventId,destinationId,firstReceivedAt,lastReceivedAt,receiptCount,effectCount:1,bodySha256}`. `FAIL` returns 503 without a receipt. `TIMEOUT` commits its receipt/effect, then waits 3.5 seconds, including for duplicates. The worker's 2-second timeout therefore leaves its remote outcome unknown even when the separate local receipt shows one effect. Signatures and signing secrets are never API display fields.

## Audit

`GET /api/audit?page=0` returns `Page<AuditItem>`, newest first with ID tie-breaking. An item is `{id,createdAt,actor,action,eventId,note}`; event ID may be null. Actors include human and worker labels. The application has no audit editing/deletion route and does not claim administrator-proof storage.

## Errors and configuration

| Status | Meaning |
| --- | --- |
| `400` | Invalid JSON, fields, payload, filters, versions, or notes |
| `401` | Missing/expired session or invalid login |
| `403` | Origin or CSRF failure |
| `404` | Missing event, destination, or route after applicable authentication |
| `409` | Version, idempotency, state, capacity, or body-hash conflict |
| `429` | Login temporarily throttled |
| `500` | Unexpected server/database failure with a generic safe message |

The API/worker/receiver read `WEBHOOK_DB_URL`. API session setup also uses `WEBHOOK_APP_ORIGIN`, `WEBHOOK_DEMO_PASSWORD`, and `WEBHOOK_COOKIE_SECURE`. Listener overrides are `WEBHOOK_API_ADDR` (default `:8080`) and `WEBHOOK_RECEIVER_ADDR` (default `:8082`). The worker and receiver share server-only `WEBHOOK_SIGNING_SECRET`; worker targets come from trusted `WEBHOOK_RECEIVER_BASE_URL`, default `http://receiver:8082`. None of those secrets belongs in a payload, browser response, or URL.

Backend integration tests use a distinct `WEBHOOK_TEST_DB_URL`. Browser checks use `WEBHOOK_BASE_URL`, the local `WEBHOOK_DEMO_PASSWORD`, and optional `WEBHOOK_BROWSER_CHANNEL`. See [operations](OPERATIONS.md) for commands, test isolation, and the worker interruption probe.
