import { useRef, useState } from "react";
import type { FormEvent } from "react";
import { ApiError, api, errorMessage, uncertainResult } from "./api";
import {
  AttemptLabel,
  AuditTable,
  Empty,
  ErrorBox,
  Heading,
  LastUpdate,
  Loading,
  StateLabel,
} from "./components";
import { useResource } from "./resource";
import type { Attempt, Delivery, DeliveryDetail } from "./types";
import {
  attemptExplanation,
  codePointLength,
  dateTime,
  redriveReason,
  validateNote,
} from "./utils";

export default function EventPage({ eventId }: { eventId: string }) {
  const resource = useResource<DeliveryDetail>(
    `/events/${encodeURIComponent(eventId)}`,
    true,
  );
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [locked, setLocked] = useState(false);
  const [mutationError, setMutationError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const inFlight = useRef(false);
  function refresh() {
    setLocked(false);
    setMutationError(null);
    setSuccess(null);
    resource.refresh();
  }
  async function redrive(event: FormEvent) {
    event.preventDefault();
    if (
      !resource.data ||
      inFlight.current ||
      locked ||
      redriveReason(resource.data.delivery)
    )
      return;
    const validation = validateNote(note);
    if (validation) {
      setMutationError(validation);
      return;
    }
    const current = resource.data;
    inFlight.current = true;
    setBusy(true);
    setMutationError(null);
    setSuccess(null);
    try {
      const delivery = await api<Delivery>(`/events/${eventId}/redrive`, {
        method: "POST",
        body: JSON.stringify({
          expectedVersion: current.delivery.version,
          note: note.trim(),
        }),
      });
      resource.replace({ ...current, delivery });
      setNote("");
      setSuccess(
        "Redrive recorded. A new cycle is queued; all earlier attempts and receipts remain available.",
      );
    } catch (cause) {
      const conflict = cause instanceof ApiError && cause.status === 409;
      const uncertain = uncertainResult(cause);
      setLocked(conflict || uncertain);
      setMutationError(
        conflict
          ? "This event changed or its redrive budget was reached. Refresh the event before continuing. Your note is retained."
          : uncertain
            ? "The redrive outcome is not confirmed. Refresh the event to check whether a new cycle was recorded before submitting again. Your note is retained."
            : errorMessage(cause),
      );
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }
  if (resource.loading)
    return <Loading>Loading event and delivery evidence...</Loading>;
  if (!resource.data)
    return (
      <ErrorBox
        message={resource.error || "The event could not be loaded."}
        onRetry={refresh}
      />
    );
  const { delivery, receipt } = resource.data;
  const attempts = [...resource.data.attempts].sort(
    (a, b) => a.number - b.number,
  );
  const unknown = attempts.some((attempt) =>
    ["TIMEOUT", "ABANDONED", "NETWORK_ERROR"].includes(attempt.status),
  );
  const reason = redriveReason(delivery);

  return (
    <>
      <a className="back-link" href="#/deliveries">
        All deliveries
      </a>
      <Heading
        title={delivery.eventType}
        description={
          <>
            <span className="event-id detail-id">{delivery.id}</span>
            <span className="detail-destination">
              {delivery.destinationName} · Created{" "}
              {dateTime(delivery.createdAt)}
            </span>
          </>
        }
        action={<StateLabel state={delivery.state} />}
      />
      <div className="event-toolbar">
        <p>
          Cycle <strong>{delivery.cycle} of 3</strong>
          <span>
            Attempts <strong>{delivery.totalAttempts} of 9</strong>
          </span>
          <span>
            Version <strong>{delivery.version}</strong>
          </span>
        </p>
        <button
          className="button secondary"
          disabled={busy || resource.loading}
          onClick={refresh}
        >
          Refresh event
        </button>
      </div>
      {resource.error && (
        <ErrorBox message={resource.error} onRetry={refresh} />
      )}
      {success && (
        <div className="notice success" role="status">
          {success}
        </div>
      )}
      {unknown && (
        <div className="notice warning">
          <strong>An attempt has an unknown remote outcome.</strong>
          <p>
            A timeout, network failure or abandoned lease does not establish
            whether the receiver processed the request. Local receipt evidence
            is shown separately below.
          </p>
        </div>
      )}
      <div className="event-layout">
        <div className="event-evidence">
          <section
            className="sheet event-summary"
            aria-labelledby="event-heading"
          >
            <div className="section-bar">
              <div className="section-title compact">
                <span className="sequence-number">01</span>
                <h2 id="event-heading">Recorded event</h2>
              </div>
              <span className="immutable-label">Immutable payload</span>
            </div>
            <dl className="event-metadata">
              <div>
                <dt>Destination</dt>
                <dd>{delivery.destinationName}</dd>
              </div>
              <div>
                <dt>Event type</dt>
                <dd>
                  <code>{delivery.eventType}</code>
                </dd>
              </div>
              <div>
                <dt>Next attempt</dt>
                <dd>
                  {delivery.nextAttemptAt
                    ? dateTime(delivery.nextAttemptAt)
                    : delivery.state === "QUEUED"
                      ? "Waiting for a worker claim"
                      : "Not scheduled"}
                </dd>
              </div>
              <div>
                <dt>Updated</dt>
                <dd>{dateTime(delivery.updatedAt)}</dd>
              </div>
            </dl>
            <details className="payload-details" open>
              <summary>Event payload</summary>
              <pre
                className="json-block"
                tabIndex={0}
                aria-label="Immutable JSON payload"
              >
                {delivery.payload}
              </pre>
            </details>
          </section>
          <section className="sheet" aria-labelledby="attempts-heading">
            <div className="section-bar">
              <div className="section-title compact">
                <span className="sequence-number">02</span>
                <h2 id="attempts-heading">Delivery attempts</h2>
              </div>
              <span className="field-help">Up to 3 per cycle</span>
            </div>
            {attempts.length === 0 ? (
              <Empty title="No attempts recorded yet">
                <p>
                  The event is waiting for a worker claim. Check queue control
                  and the recorded worker heartbeat.
                </p>
              </Empty>
            ) : (
              <ol className="attempt-timeline">
                {attempts.map((attempt) => (
                  <li key={attempt.id}>
                    <AttemptRecord attempt={attempt} />
                  </li>
                ))}
              </ol>
            )}
          </section>
          <section className="sheet" aria-labelledby="receipt-heading">
            <div className="section-bar">
              <div className="section-title compact">
                <span className="sequence-number">03</span>
                <h2 id="receipt-heading">Local receiver evidence</h2>
              </div>
            </div>
            {receipt ? (
              <div className="receipt-evidence">
                <div className="receipt-counts">
                  <div>
                    <strong>{receipt.effectCount}</strong>
                    <span>Synthetic effect recorded</span>
                  </div>
                  <div>
                    <strong>{receipt.receiptCount}</strong>
                    <span>
                      {receipt.receiptCount === 1 ? "Receipt" : "Receipts"}{" "}
                      recorded
                    </span>
                  </div>
                </div>
                <p>
                  This is the locally observable demo receiver record. A
                  timed-out sender attempt keeps its unknown outcome even when
                  this record exists.
                </p>
                <dl className="receipt-metadata">
                  <div>
                    <dt>First received</dt>
                    <dd>{dateTime(receipt.firstReceivedAt)}</dd>
                  </div>
                  <div>
                    <dt>Last received</dt>
                    <dd>{dateTime(receipt.lastReceivedAt)}</dd>
                  </div>
                  <div>
                    <dt>Body SHA-256</dt>
                    <dd>
                      <code>{receipt.bodySha256}</code>
                    </dd>
                  </div>
                </dl>
              </div>
            ) : (
              <div className="receipt-empty">
                <h3>No local receipt recorded</h3>
                <p>
                  The receiver has not recorded a synthetic effect for this
                  event. Absence of a receipt here is separate from the sender’s
                  attempt status.
                </p>
              </div>
            )}
          </section>
        </div>
        <aside
          className="sheet redrive-panel"
          aria-labelledby="redrive-heading"
        >
          <div className="section-bar">
            <h2 id="redrive-heading">Redrive control</h2>
          </div>
          <div className="panel-body">
            <p className="budget-label">
              {delivery.redrives} of 2 redrives used
            </p>
            <p className="field-help">
              A redrive starts a new cycle with up to 3 attempts. The event
              payload stays unchanged.
            </p>
            {mutationError && (
              <div className="notice error" role="alert">
                <p>{mutationError}</p>
                {locked && (
                  <button className="button secondary" onClick={refresh}>
                    Refresh before continuing
                  </button>
                )}
              </div>
            )}
            {reason ? (
              <p className="read-only-message">{reason}</p>
            ) : (
              <form className="stack-form" onSubmit={redrive}>
                <div className="field">
                  <label htmlFor="redrive-note">Redrive note</label>
                  <textarea
                    id="redrive-note"
                    rows={5}
                    value={note}
                    required
                    disabled={busy || locked}
                    onChange={(event) => setNote(event.target.value)}
                    aria-describedby="redrive-help"
                  />
                  <p className="field-help" id="redrive-help">
                    10-500 characters, excluding outer spaces.
                    <span className="byte-count">
                      {codePointLength(note)} / 500
                    </span>
                  </p>
                </div>
                <button className="button primary" disabled={busy || locked}>
                  {busy ? "Recording redrive..." : "Queue a new cycle"}
                </button>
              </form>
            )}
            <a href="#/receivers" className="panel-link">
              Check demo receiver modes
            </a>
          </div>
        </aside>
      </div>
      <section
        className="sheet event-audit"
        aria-labelledby="event-audit-heading"
      >
        <div className="section-bar">
          <h2 id="event-audit-heading">Event audit history</h2>
        </div>
        {resource.data.audit.length ? (
          <AuditTable items={resource.data.audit} />
        ) : (
          <p className="inline-empty">No event audit records were returned.</p>
        )}
      </section>
      <LastUpdate value={resource.updatedAt} polling />
    </>
  );
}

function AttemptRecord({ attempt }: { attempt: Attempt }) {
  return (
    <article className="attempt-record">
      <div className="attempt-marker" aria-hidden="true">
        {attempt.number}
      </div>
      <div className="attempt-content">
        <div className="attempt-heading">
          <h3>
            Attempt {attempt.number} <span>Cycle {attempt.cycle}</span>
          </h3>
          <AttemptLabel status={attempt.status} />
        </div>
        <p className="attempt-explanation">{attemptExplanation(attempt)}</p>
        <dl className="attempt-facts">
          <div>
            <dt>Started</dt>
            <dd>{dateTime(attempt.startedAt)}</dd>
          </div>
          <div>
            <dt>Finished</dt>
            <dd>{dateTime(attempt.finishedAt)}</dd>
          </div>
          <div>
            <dt>HTTP status</dt>
            <dd>
              <code>{attempt.httpStatus ?? "Not recorded"}</code>
            </dd>
          </div>
          <div>
            <dt>Duration</dt>
            <dd>
              {attempt.durationMs === null
                ? "Not recorded"
                : `${attempt.durationMs.toLocaleString("en-GB")} ms`}
            </dd>
          </div>
          {attempt.errorCategory && (
            <div>
              <dt>Error category</dt>
              <dd>{attempt.errorCategory}</dd>
            </div>
          )}
          <div>
            <dt>Worker</dt>
            <dd>
              <code>{attempt.workerId}</code>
            </dd>
          </div>
        </dl>
        {attempt.responsePreview !== null && (
          <details className="response-details">
            <summary>Response preview</summary>
            <pre tabIndex={0}>
              {attempt.responsePreview || "(Empty response body)"}
            </pre>
            <p className="field-help">Text preview limited to 2,048 bytes.</p>
          </details>
        )}
      </div>
    </article>
  );
}
