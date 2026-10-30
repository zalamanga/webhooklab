import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import { ApiError, api, errorMessage, uncertainResult } from "./api";
import {
  Empty,
  ErrorBox,
  Heading,
  LastUpdate,
  Loading,
  Pagination,
  StateLabel,
} from "./components";
import { useResource } from "./resource";
import type { Dashboard, Delivery, Page, QueueControl, State } from "./types";
import { dateTime, pageNumber, stateLabels } from "./utils";

export default function DeliveriesPage({ query }: { query: URLSearchParams }) {
  const state = query.get("state") || "ALL";
  const search = query.get("search") || "";
  const [searchInput, setSearchInput] = useState(search);
  const [queueBusy, setQueueBusy] = useState(false);
  const [queueLocked, setQueueLocked] = useState(false);
  const [queueError, setQueueError] = useState<string | null>(null);
  const [queueSuccess, setQueueSuccess] = useState<string | null>(null);
  const queueRequest = useRef(false);
  const dashboard = useResource<Dashboard>("/dashboard", true);
  const params = new URLSearchParams({
    page: String(pageNumber(query)),
    state,
    search,
  });
  const events = useResource<Page<Delivery>>(`/events?${params}`, true);
  useEffect(() => {
    setSearchInput(search);
  }, [search]);

  function filter(change: Record<string, string>) {
    window.location.hash = `/deliveries?${new URLSearchParams({ state, search, page: "0", ...change })}`;
  }
  function refresh() {
    setQueueLocked(false);
    setQueueError(null);
    setQueueSuccess(null);
    dashboard.refresh();
    events.refresh();
  }
  async function toggleQueue() {
    if (!dashboard.data || queueRequest.current || queueLocked) return;
    const current = dashboard.data;
    queueRequest.current = true;
    setQueueBusy(true);
    setQueueError(null);
    setQueueSuccess(null);
    try {
      const queue = await api<QueueControl>("/queue", {
        method: "PUT",
        body: JSON.stringify({
          expectedVersion: current.queue.version,
          paused: !current.queue.paused,
        }),
      });
      dashboard.replace({ ...current, queue });
      setQueueSuccess(
        queue.paused
          ? "New claims are paused. An existing request may still finish."
          : "New claims are enabled. Available workers can pick up queued events.",
      );
    } catch (cause) {
      const conflict = cause instanceof ApiError && cause.status === 409;
      setQueueLocked(conflict || uncertainResult(cause));
      setQueueError(
        conflict
          ? "Queue control changed. Refresh the workspace before changing it again."
          : uncertainResult(cause)
            ? "The control outcome is not confirmed. Refresh the workspace to read the recorded queue state before continuing."
            : errorMessage(cause),
      );
    } finally {
      queueRequest.current = false;
      setQueueBusy(false);
    }
  }

  return (
    <>
      <Heading
        title="Deliveries"
        description="Follow each event from the outbox to its attempts and receiver evidence."
        action={
          <a href="#/send" className="button primary">
            Send an event
          </a>
        }
      />
      <section className="control-sheet sheet" aria-labelledby="queue-heading">
        <div>
          <h2 id="queue-heading">Queue control</h2>
          <p>
            <strong>
              {dashboard.data
                ? dashboard.data.queue.paused
                  ? "New claims paused"
                  : "New claims enabled"
                : dashboard.error
                  ? "Queue state unavailable"
                  : "Reading queue state..."}
            </strong>
          </p>
          <p className="field-help">
            Pause stops new claims. Requests already in flight may finish; lease
            recovery continues.
          </p>
        </div>
        <div className="worker-evidence">
          <h3>Worker heartbeat</h3>
          {dashboard.data ? (
            <>
              <p>
                {dashboard.data.workers.activeCount > 0
                  ? `${dashboard.data.workers.activeCount} ${dashboard.data.workers.activeCount === 1 ? "worker" : "workers"} seen within the last 15 seconds`
                  : dashboard.data.workers.lastSeenAt
                    ? "No worker seen within the last 15 seconds"
                    : "No worker heartbeat recorded yet"}
              </p>
              <p className="field-help">
                Latest: {dateTime(dashboard.data.workers.lastSeenAt)}
              </p>
            </>
          ) : (
            <p className="muted">Waiting for recorded heartbeat evidence.</p>
          )}
        </div>
        <button
          className="button secondary"
          onClick={toggleQueue}
          disabled={
            !dashboard.data || queueBusy || queueLocked || dashboard.loading
          }
        >
          {queueBusy
            ? "Saving queue control..."
            : dashboard.data?.queue.paused
              ? "Resume new claims"
              : "Pause new claims"}
        </button>
      </section>
      {queueError && <ErrorBox message={queueError} onRetry={refresh} />}
      {queueSuccess && (
        <div className="notice success" role="status">
          {queueSuccess}
        </div>
      )}
      {dashboard.error && (
        <ErrorBox message={dashboard.error} onRetry={refresh} />
      )}
      {dashboard.loading ? (
        <Loading>Loading delivery totals...</Loading>
      ) : (
        dashboard.data && (
          <div className="delivery-totals" aria-label="All delivery totals">
            <button
              className={state === "ALL" ? "active" : ""}
              onClick={() => filter({ state: "ALL", search: "" })}
              aria-pressed={state === "ALL"}
            >
              <span>All events</span>
              <strong>{dashboard.data.total}</strong>
            </button>
            {(Object.entries(stateLabels) as [State, string][]).map(
              ([value, label]) => (
                <button
                  key={value}
                  className={state === value ? "active" : ""}
                  onClick={() => filter({ state: value, search: "" })}
                  aria-pressed={state === value}
                >
                  <span>{label}</span>
                  <strong>{dashboard.data!.counts[value]}</strong>
                </button>
              ),
            )}
          </div>
        )
      )}
      <section className="sheet" aria-labelledby="register-heading">
        <div className="section-bar">
          <div>
            <h2 id="register-heading">Event register</h2>
            <p className="field-help">
              An event and its first queued delivery are recorded together.
            </p>
          </div>
          <button
            className="button text-button"
            onClick={refresh}
            disabled={dashboard.loading || events.loading || queueBusy}
          >
            Refresh workspace
          </button>
        </div>
        <form
          className="register-filters"
          onSubmit={(event: FormEvent) => {
            event.preventDefault();
            filter({ search: searchInput.trim() });
          }}
        >
          <div className="field search-field">
            <label htmlFor="event-search">Event ID or type</label>
            <div className="search-control">
              <input
                id="event-search"
                value={searchInput}
                maxLength={64}
                onChange={(event) => setSearchInput(event.target.value)}
                placeholder="Find an event"
              />
              <button className="button secondary" type="submit">
                Search
              </button>
            </div>
          </div>
          <div className="field">
            <label htmlFor="state-filter">Delivery state</label>
            <select
              id="state-filter"
              value={state}
              onChange={(event) => filter({ state: event.target.value })}
            >
              <option value="ALL">All states</option>
              {Object.entries(stateLabels).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </div>
          {(state !== "ALL" || search) && (
            <button
              className="button text-button clear-filters"
              type="button"
              onClick={() => filter({ state: "ALL", search: "" })}
            >
              Clear filters
            </button>
          )}
        </form>
        {events.error && (
          <ErrorBox message={events.error} onRetry={events.refresh} />
        )}
        {events.loading ? (
          <Loading>Loading delivery records...</Loading>
        ) : events.data && events.data.items.length === 0 ? (
          <Empty
            title={
              search || state !== "ALL"
                ? "No events match these filters"
                : "No events recorded yet"
            }
          >
            <p>
              {search || state !== "ALL"
                ? "Change the state or search to see other deliveries."
                : "Send a synthetic event to begin inspecting its attempts."}
            </p>
            {search || state !== "ALL" ? (
              <button
                className="button secondary"
                onClick={() => filter({ state: "ALL", search: "" })}
              >
                Show all events
              </button>
            ) : (
              <a href="#/send" className="button primary">
                Send an event
              </a>
            )}
          </Empty>
        ) : (
          events.data && (
            <>
              <div
                className="table-scroll"
                role="region"
                aria-label="Delivery register"
                tabIndex={0}
              >
                <table className="deliveries-table">
                  <thead>
                    <tr>
                      <th scope="col">Event / created</th>
                      <th scope="col">Destination</th>
                      <th scope="col">State</th>
                      <th scope="col" className="numeric">
                        Cycle
                      </th>
                      <th scope="col" className="numeric">
                        Attempts
                      </th>
                      <th scope="col">Next attempt</th>
                    </tr>
                  </thead>
                  <tbody>
                    {events.data.items.map((delivery) => (
                      <tr key={delivery.id}>
                        <th scope="row">
                          <a
                            className="row-link event-id"
                            href={`#/events/${delivery.id}`}
                          >
                            {delivery.id}
                          </a>
                          <span className="row-meta">
                            {delivery.eventType} ·{" "}
                            {dateTime(delivery.createdAt)}
                          </span>
                        </th>
                        <td>{delivery.destinationName}</td>
                        <td>
                          <StateLabel state={delivery.state} />
                        </td>
                        <td className="numeric">{delivery.cycle} / 3</td>
                        <td className="numeric">
                          {delivery.totalAttempts} / 9
                          <span className="row-meta">
                            {delivery.cycleAttempts} in this cycle
                          </span>
                        </td>
                        <td className="next-attempt">
                          {delivery.nextAttemptAt
                            ? dateTime(delivery.nextAttemptAt)
                            : "Not scheduled"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <Pagination
                page={events.data}
                label="events"
                onChange={(page) => filter({ page: String(page) })}
              />
            </>
          )
        )}
      </section>
      <LastUpdate value={dashboard.updatedAt} polling />
      <p className="table-note">
        A timeout or abandoned attempt leaves the remote outcome unknown to the
        sender. Open the event to compare attempts with the locally observable
        receiver receipt.
      </p>
    </>
  );
}
