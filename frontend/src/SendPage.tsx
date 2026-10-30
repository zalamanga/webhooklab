import { useRef } from "react";
import type { Dispatch, FormEvent, SetStateAction } from "react";
import { api, creationFailureStatus, errorMessage } from "./api";
import type { CreationStatus } from "./api";
import { ErrorBox, Heading, Loading } from "./components";
import { useResource } from "./resource";
import type {
  Delivery,
  Destination,
  DestinationId,
  EventType,
  User,
} from "./types";
import { modeLabels, payloadBytes, validatePayload } from "./utils";

export interface EventDraft {
  destinationId: DestinationId;
  eventType: EventType;
  payload: string;
  key: string | null;
  actorId: string | null;
  status: CreationStatus;
  error: string | null;
}
export const newEventDraft = (): EventDraft => ({
  destinationId: "warehouse",
  eventType: "order.created",
  payload: '{\n  "orderId": "synthetic-order-001"\n}',
  key: null,
  actorId: null,
  status: "idle",
  error: null,
});

export default function SendPage({
  user,
  draft,
  setDraft,
  onCreated,
}: {
  user: User;
  draft: EventDraft;
  setDraft: Dispatch<SetStateAction<EventDraft>>;
  onCreated: (delivery: Delivery) => void;
}) {
  const destinations = useResource<Destination[]>("/destinations");
  const inFlight = useRef(false);
  const locked =
    draft.status === "pending" ||
    draft.status === "uncertain" ||
    draft.status === "conflict";
  const differentActor = draft.actorId !== null && draft.actorId !== user.id;
  const selected = destinations.data?.find(
    (item) => item.id === draft.destinationId,
  );
  function update(change: Partial<EventDraft>) {
    if (locked) return;
    setDraft((current) => ({
      ...current,
      ...change,
      status: "idle",
      error: null,
      key: null,
      actorId: null,
    }));
  }
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (
      inFlight.current ||
      draft.status === "pending" ||
      differentActor ||
      !destinations.data
    )
      return;
    const validation = validatePayload(draft.payload);
    if (validation) {
      setDraft((current) => ({ ...current, error: validation }));
      return;
    }
    const key = draft.key ?? crypto.randomUUID();
    const body = JSON.stringify({
      destinationId: draft.destinationId,
      eventType: draft.eventType,
      payload: draft.payload,
    });
    inFlight.current = true;
    setDraft((current) => ({
      ...current,
      key,
      actorId: user.id,
      status: "pending",
      error: null,
    }));
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 15_000);
    try {
      const delivery = await api<Delivery>("/events", {
        method: "POST",
        headers: { "Idempotency-Key": key },
        body,
        signal: controller.signal,
      });
      setDraft(newEventDraft());
      onCreated(delivery);
    } catch (cause) {
      setDraft((current) => ({
        ...current,
        status: creationFailureStatus(draft.status, cause),
        error: controller.signal.aborted
          ? "The server did not confirm event creation within 15 seconds."
          : errorMessage(cause),
      }));
    } finally {
      window.clearTimeout(timeout);
      inFlight.current = false;
    }
  }

  return (
    <>
      <Heading
        title="Send an event"
        description="Record a synthetic event and its initial queued delivery in one transaction."
      />
      <div className="send-layout">
        <section
          className="sheet send-form-section"
          aria-labelledby="compose-heading"
        >
          <div className="section-title">
            <span className="sequence-number">01</span>
            <h2 id="compose-heading">Compose the event</h2>
          </div>
          {destinations.loading ? (
            <Loading>Loading fixed destinations...</Loading>
          ) : destinations.error ? (
            <ErrorBox
              message={destinations.error}
              onRetry={destinations.refresh}
            />
          ) : !destinations.data?.length ? (
            <ErrorBox
              message="No demo destinations were returned. Refresh before recording an event."
              onRetry={destinations.refresh}
            />
          ) : (
            <form className="stack-form" onSubmit={submit}>
              {differentActor && (
                <div className="notice error" role="alert">
                  <strong>This draft belongs to another account.</strong>
                  <p>
                    Sign in with the account that first submitted this event to
                    retry it safely.
                  </p>
                </div>
              )}
              <div className="form-pair">
                <div className="field">
                  <label htmlFor="destination">Destination</label>
                  <select
                    id="destination"
                    value={draft.destinationId}
                    disabled={locked}
                    onChange={(event) =>
                      update({
                        destinationId: event.target.value as DestinationId,
                      })
                    }
                  >
                    {destinations.data?.map((item) => (
                      <option key={item.id} value={item.id}>
                        {item.name}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="field">
                  <label htmlFor="event-type">Event type</label>
                  <select
                    id="event-type"
                    value={draft.eventType}
                    disabled={locked}
                    onChange={(event) =>
                      update({ eventType: event.target.value as EventType })
                    }
                  >
                    <option value="order.created">order.created</option>
                    <option value="order.cancelled">order.cancelled</option>
                  </select>
                </div>
              </div>
              {selected && (
                <div className="destination-context">
                  <code>{selected.route}</code>
                  <span>
                    Current demo mode:{" "}
                    <strong>{modeLabels[selected.mode]}</strong>
                  </span>
                  <a href="#/receivers">Manage demo modes</a>
                </div>
              )}
              <div className="field">
                <label htmlFor="event-payload">JSON payload</label>
                <textarea
                  id="event-payload"
                  className="payload-input"
                  rows={10}
                  value={draft.payload}
                  disabled={locked}
                  required
                  spellCheck={false}
                  autoCapitalize="off"
                  autoCorrect="off"
                  onChange={(event) => update({ payload: event.target.value })}
                  aria-describedby="payload-help"
                />
                <p className="field-help" id="payload-help">
                  JSON object, up to 16 KiB in UTF-8. The initial value is a
                  synthetic sample.
                  <span className="byte-count">
                    {payloadBytes(draft.payload).toLocaleString("en-GB")} /
                    16,384 bytes
                  </span>
                </p>
              </div>
              {draft.error && (
                <div className="notice error" role="alert">
                  <strong>
                    {draft.status === "uncertain"
                      ? "Creation outcome is not confirmed"
                      : draft.status === "conflict"
                        ? "Request identity or capacity conflict"
                        : "Event could not be created"}
                  </strong>
                  <p>{draft.error}</p>
                  {draft.status === "conflict" && (
                    <p>
                      This local workspace is limited to 500 events. An
                      already-recorded request identity can still be replayed at
                      capacity.
                    </p>
                  )}
                  {locked && (
                    <p>
                      The destination, type, payload and request identity are
                      retained. Retry sends this same event; it does not assign
                      a new identity.
                    </p>
                  )}
                </div>
              )}
              <div className="form-actions">
                <button
                  className="button primary"
                  disabled={
                    draft.status === "pending" ||
                    differentActor ||
                    !destinations.data
                  }
                >
                  {draft.status === "pending"
                    ? "Recording event..."
                    : draft.status === "uncertain" ||
                        draft.status === "conflict"
                      ? "Retry the same event"
                      : "Record and queue event"}
                </button>
                <p className="field-help">
                  A paused queue accepts new events and waits to claim them.
                </p>
              </div>
              {draft.status === "pending" && (
                <p role="status" className="field-help">
                  Waiting for the server to confirm the event. Delivery attempts
                  happen separately in the worker.
                </p>
              )}
              {draft.key && (
                <details className="request-details">
                  <summary>Request identity</summary>
                  <code>{draft.key}</code>
                  <p className="field-help">
                    Keep this tab open until the creation outcome is confirmed.
                  </p>
                </details>
              )}
            </form>
          )}
        </section>
        <aside className="send-guide">
          <div className="section-title">
            <span className="sequence-number">02</span>
            <h2>What happens next</h2>
          </div>
          <ol className="delivery-sequence">
            <li>
              <strong>Event recorded</strong>
              <p>
                The immutable payload and queued delivery are committed
                together.
              </p>
            </li>
            <li>
              <strong>Worker attempts delivery</strong>
              <p>
                Up to 3 attempts in a cycle, with retry waits of 2 and 5
                seconds.
              </p>
            </li>
            <li>
              <strong>Inspect receiver evidence</strong>
              <p>
                A timeout can leave the sender uncertain even when the local
                receiver recorded one effect.
              </p>
            </li>
          </ol>
          <div className="guide-note">
            <h3>Local destinations only</h3>
            <p>
              Warehouse and Billing are fixed demo receivers. This lab does not
              change business orders or move money.
            </p>
          </div>
        </aside>
      </div>
    </>
  );
}
