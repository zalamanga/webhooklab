# WebhookLab implementation contract

This file is the shared boundary for backend, frontend and infrastructure. Resolve changes with the project owner before changing payloads or semantics.

## Scope and services

Local synthetic integration laboratory. Next.js App Router/TypeScript builds a static export; Nginx serves it and proxies `/api/` to a Go API. A separate Go worker sends requests to a separate Go demo receiver. PostgreSQL 17 persists the event/outbox, attempts, receiver receipts and control/audit data. No RabbitMQ, external payment integration, arbitrary destination URLs or external email are included.

The Next.js UI is a real Next.js build/export, with a client-side hash router. No SSR/server actions are claimed. API base is `/api`. Web origin is exactly `http://127.0.0.1:8097`. Live PostgreSQL is loopback 5448; test PostgreSQL is isolated loopback 5449. API listens 8080, receiver 8082, internally only. Both `/api/health` and receiver `/health` check their database connection.

Backend owner: all `backend/` including Go modules, commands, migration, tests and backend Dockerfile. Frontend owner: all `frontend/` except Dockerfile/Nginx/CSP build helper (root owns those). Docs owner: README and docs except CONTRACT, DESIGN, UX-REVIEW, VERIFICATION (root). Root owns Compose, scripts, CI, publication, screenshots, portfolio and final gate. Do not touch other projects or global tools/config.

## Auth and transport

One local workspace operator account: `operator@webhooklab.test`. Seed only when users table is empty from `WEBHOOK_DEMO_PASSWORD`; no public default password and do not reset an existing account. Password BCrypt cost12. `WEBHOOK_DB_URL` is the database connection; `WEBHOOK_APP_ORIGIN` exact origin; `WEBHOOK_SIGNING_SECRET` is server-only shared master secret for worker/receiver. `WEBHOOK_RECEIVER_BASE_URL` is trusted deployment configuration, default `http://receiver:8082`; user input cannot change it.

Opaque random session token, SHA256 digest stored in PostgreSQL, 12h expiry. Cookie `webhooklab_session`, HttpOnly, SameSite=Lax, Path=/, Secure controlled by `WEBHOOK_COOKIE_SECURE` (local false). JSON authenticated mutations require exact configured Origin and `X-CSRF-Token` from the session response. Login requires Origin and JSON; logout POST also requires CSRF. Do not log password, cookie, signing secret, CSRF or request bodies. Login throttle persists failed attempts by remote address; document the local reverse-proxy scope.

Protect every API-server route except exact GET `/api/health` and POST `/api/auth/login`; normalized/prefix/matrix variants must not bypass auth. Only public receiver routes are GET `/health` and POST `/hooks/warehouse`, POST `/hooks/billing`, with HMAC validation on hooks. No public receiver management route.

Standard JSON error: `{code:string,message:string}` with safe messages. 400 validation, 401 unauthenticated, 403 origin/CSRF, 404 missing, 409 version/idempotency/state/capacity conflict. Reject trailing JSON, wrong types, unknown outer fields and fractional versions. UUID IDs and Idempotency-Key use canonical UUID format. Dates are RFC3339 UTC. Body <=32 KiB. Page size fixed25, page zero-based, stable ordering. Do not return secrets in responses or error messages.

## Delivery semantics

States: `QUEUED`, `SENDING`, `RETRY_WAIT`, `DELIVERED`, `DEAD_LETTER`.

Create event accepts `{destinationId:"warehouse"|"billing",eventType:"order.created"|"order.cancelled",payload:string}`. Payload is JSON text for a UTF8 object, nonempty, <=16 KiB. Preserve compact original numeric representations; never parse arbitrary numbers into float64. Envelope stored/returned: `{id,eventType,createdAt,payload:<original object>}` as immutable bytes to sign/send. Payload itself remains a JSON text string in API representations. No payment or business-order state is mutated by this app.

The event and its initial QUEUED delivery are committed in one database transaction (outbox). Import/create Idempotency-Key is mandatory, scoped to actor. Same key and same destination/type/compact payload returns the same current delivery HTTP200; changed content409; new201. Fingerprint must be unambiguous (length-delimited or canonical object), not concatenated ambiguous strings. At most500 events, checked under a singleton database lock; idempotent replay still works at capacity.

One cycle has3 attempts, retry delays2s then5s. Request timeout2s, lease10s. A claim stores a STARTED attempt and SENDING lease token in a short transaction using row locks/SKIP LOCKED, commits before network I/O. Do not hold database transactions open across HTTP. Attempt statuses: `STARTED`, `SUCCEEDED`, `HTTP_ERROR`, `TIMEOUT`, `NETWORK_ERROR`, `ABANDONED`. Any2xx is success. Other HTTP statuses, timeout or network error retry until attempt3 then DEAD_LETTER. Bounded response preview <=2048 bytes, textual display only; status/duration/errorCategory are stored. Never store request authentication headers or signing secrets.

Lease recovery marks abandoned STARTED attempts ABANDONED, outcome unknown; exhausted budget becomes DEAD_LETTER, otherwise RETRY_WAIT. Lease token/version fences stale worker completions so they cannot overwrite a newer claim. Receiver effect may have happened before a timeout/crash; do not claim exactly-once delivery. Receiver deduplication is the tested local effect guarantee.

DEAD_LETTER can be redriven with `{expectedVersion,note}` (trimmed10-500 Unicode codepoints) at most2 times per event, resetting the cycle budget but retaining all prior attempts/receipts. Max9 attempts across3 cycles. Concurrent/stale redrive409; do not create duplicate transitions/audit. All meaningful create/claim/result/recovery/redrive/control changes record audit atomically with their corresponding transition. Source payload and prior attempts are immutable except completing STARTED or marking it ABANDONED.

Queue pause stops new claims, while an existing request may finish. Use a shared queue-control row lock to linearize pause with claims. Expired leases still recover while paused. Worker heartbeat continues while paused; dashboard says "seen within the last15seconds", not guaranteed live health. Heartbeat every3s; latest/active count from database clock, stale15s. Support multiple actual workers safely, regardless of local Compose default1.

## Demo receiver and signing

Fixed destinations: warehouse=`Warehouse demo`, billing=`Billing demo`. Their routes are `/hooks/warehouse`, `/hooks/billing`; API shows only relative route, never configurable host/secret. Both initial modes HEALTHY. Modes `HEALTHY`, `FAIL`, `TIMEOUT` with versioned DB configuration through the authenticated API.

- HEALTHY verifies signature and stores/returns a receipt immediately.
- FAIL verifies signature, returns503 without applying the effect.
- TIMEOUT verifies signature, commits the receipt/effect, then waits3500ms before responding, deliberately exceeding the worker2s timeout. Duplicate requests in this mode also wait. After switching to HEALTHY, a redrive returns a duplicate receipt without a second effect.

Headers: `Webhook-Id`=event UUID, `Webhook-Timestamp`=Unix seconds, `Webhook-Signature`=`v1=<hexHMACSHA256>`. Signing input is timestamp + `.` + eventId + `.` + exact envelope bytes. Per-destination key is HMAC-SHA256(master secret, destinationId), use raw derived bytes for envelope HMAC. Receiver uses constant-time comparison, timestamp tolerance300s and size/ID/envelope-ID validation before persisting. Same destination+eventId with different body SHA256 returns409. First successful receipt creates one effect; duplicates increment receipt count only. Schema unique constraint/transaction handles concurrent duplicates. No secret or actual signature in UI/audit logs.

Receipt represents synthetic receiver processing, not real business transactions: `{eventId,destinationId,firstReceivedAt,lastReceivedAt,receiptCount,effectCount:1,bodySha256}`. Receiver FAIL produces no receipt. TIMEOUT attempt remains unknown from the sender's view even when the locally observable receipt shows one effect. Response JSON `{received:true,eventId,duplicate:boolean}`.

## API response types

`User={id:string,email:string,name:string}`. `Session={user:User,csrfToken:string}`.

`QueueControl={paused:boolean,version:number}`.

`WorkerStatus={activeCount:number,lastSeenAt:string|null,staleAfterSeconds:15}`.

`Dashboard={total:number,counts:{QUEUED:number,SENDING:number,RETRY_WAIT:number,DELIVERED:number,DEAD_LETTER:number},queue:QueueControl,workers:WorkerStatus,serverTime:string}`. Count source is real delivery rows, no seeded fictitious performance metrics.

`Destination={id:"warehouse"|"billing",name:string,route:string,mode:"HEALTHY"|"FAIL"|"TIMEOUT",version:number}`.

`Delivery={id:string,eventType:string,destinationId:string,destinationName:string,payload:string,state:State,version:number,cycle:number,cycleAttempts:number,totalAttempts:number,redrives:number,nextAttemptAt:string|null,createdAt:string,updatedAt:string}`. `id` is the stable event/delivery UUID, cycle starts1. No second separate deliveryId in UI.

`Attempt={id:string,number:number,cycle:number,status:AttemptStatus,startedAt:string,finishedAt:string|null,httpStatus:number|null,durationMs:number|null,responsePreview:string|null,errorCategory:string|null,workerId:string}`. number monotonically grows across cycles. Error category is safe fixed vocabulary rather than raw network error (which could contain internals).

`Receipt` as above. `AuditItem={id:string,createdAt:string,actor:string,action:string,eventId:string|null,note:string}`; human and worker actor labels, no secret data. `Page<T>={items:T[],page:number,pageSize:25,total:number}`.

`DeliveryDetail={delivery:Delivery,attempts:Attempt[],receipt:Receipt|null,audit:AuditItem[]}`. Bounded by max9 attempts and stored event actions.

## Routes

- GET `/api/health` -> `{status:"ok"}`.
- POST `/api/auth/login` `{email,password}` -> Session200 + cookie.
- GET `/api/auth/session` -> Session200.
- POST `/api/auth/logout` ->204, expires cookie and revokes digest.
- GET `/api/dashboard` -> Dashboard200.
- GET `/api/destinations` -> Destination[]200.
- PUT `/api/queue` `{expectedVersion,paused}` -> QueueControl200.
- PUT `/api/destinations/{id}` `{expectedVersion,mode}` -> Destination200.
- GET `/api/events?page=0&state=ALL&search=` -> Page<Delivery>. state=ALL or exact state; searchtrim0-64 literal substring of ID or eventType, escape SQL wildcards. Sort createdAt desc,id desc.
- POST `/api/events` as above + Idempotency-Key -> Delivery201/200.
- GET `/api/events/{id}` -> DeliveryDetail200.
- POST `/api/events/{id}/redrive` `{expectedVersion,note}` -> Delivery200.
- GET `/api/audit?page=0` -> Page<AuditItem>200, createdAt desc,id desc.

No deletes, account signup, arbitrary endpoint editor, signature display, additional API aliases or external send buttons.

## Frontend behavior

Light orderly operations UI according to DESIGN.md. Views: deliveries/dashboard, send event, event detail, demo receivers, audit. Hash routes root `#/deliveries`, `#/send`, `#/events/{uuid}`, `#/receivers`, `#/audit`. Real server state, manual refresh plus conservative automatic polling every2s only on active visible detail/dashboard; abort/cleanup old polls and never overlap. Do not auto-erase forms on poll. Show last update/worker stale text and distinguish unknown outcome after timeout.

Create request state retains the same UUID idempotency key, destination/type/payload and actor while an outcome is uncertain; lock editing, safe retry exact bytes. A known400 releases the fields and changing input creates a new key. Session expiry keeps uncertain draft in current tab memory, only its initiating user can retry. Do not put secrets/payloads in localStorage or URL. A409 on mutation disables further stale editing until explicit refresh; preserve notes. Guard overlapping submissions. Show friendly message for bounded redrive budget/capacity.

Authentication, input validation, loading, empty, request errors, unknown outcome, stale version, paused queue and expired session are visible states. All controls must work, keyboard focus visible, mobile no page overflow; native details and radio/select controls preferred to custom popovers. Payload/response previews render as text, never HTML. Forms do not prefill password.

Tests include real PostgreSQL behavior/concurrent claims/fencing/leases/retry/deadletter/redrive/receiver dedupe/signatures/auth plus browser real success/failure/timeout with1effect, pause/resume, retry after dropped response, responsive/keyboard/filter/audit paths. Browser tests may use controlled responses to expose specific network states, but success records/screenshots come from actual services. Do not commit generated credentials, DB volumes, node_modules, build caches or failure traces.
