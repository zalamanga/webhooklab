import { useRef, useState } from "react";
import { ApiError, api, errorMessage, uncertainResult } from "./api";
import { ErrorBox, Heading, LastUpdate, Loading } from "./components";
import { useResource } from "./resource";
import type { Destination, DestinationId, ReceiverMode } from "./types";
import { modeLabels } from "./utils";

export default function ReceiversPage() {
  const resource = useResource<Destination[]>("/destinations");
  const [selected, setSelected] = useState<
    Partial<Record<DestinationId, ReceiverMode>>
  >({});
  const [locked, setLocked] = useState<Partial<Record<DestinationId, boolean>>>(
    {},
  );
  const [errors, setErrors] = useState<Partial<Record<DestinationId, string>>>(
    {},
  );
  const [success, setSuccess] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<DestinationId | null>(null);
  const inFlight = useRef(false);
  function refresh() {
    setSelected({});
    setLocked({});
    setErrors({});
    setSuccess(null);
    resource.refresh();
  }
  async function save(destination: Destination) {
    if (!resource.data || inFlight.current || locked[destination.id]) return;
    const mode = selected[destination.id] ?? destination.mode;
    if (mode === destination.mode) return;
    const current = resource.data;
    inFlight.current = true;
    setBusyId(destination.id);
    setSuccess(null);
    setErrors((value) => ({ ...value, [destination.id]: undefined }));
    try {
      const updated = await api<Destination>(
        `/destinations/${destination.id}`,
        {
          method: "PUT",
          body: JSON.stringify({ expectedVersion: destination.version, mode }),
        },
      );
      resource.replace(
        current.map((item) => (item.id === updated.id ? updated : item)),
      );
      setSelected((value) => ({ ...value, [updated.id]: updated.mode }));
      setSuccess(
        `${updated.name} now uses ${modeLabels[updated.mode].toLowerCase()} mode.`,
      );
    } catch (cause) {
      const conflict = cause instanceof ApiError && cause.status === 409;
      const uncertain = uncertainResult(cause);
      if (conflict || uncertain)
        setLocked((value) => ({ ...value, [destination.id]: true }));
      setErrors((value) => ({
        ...value,
        [destination.id]: conflict
          ? "This receiver configuration changed. Refresh receiver modes before editing again."
          : uncertain
            ? "The mode change is not confirmed. Refresh receiver modes to read the recorded setting before continuing."
            : errorMessage(cause),
      }));
    } finally {
      inFlight.current = false;
      setBusyId(null);
    }
  }

  return (
    <>
      <Heading
        title="Demo receivers"
        description="Choose controlled local behavior, then inspect how the worker records each attempt."
        action={
          <button
            className="button secondary"
            onClick={refresh}
            disabled={resource.loading || busyId !== null}
          >
            Refresh receiver modes
          </button>
        }
      />
      <div className="notice">
        <strong>Changes affect future incoming requests.</strong>
        <p>
          They do not rewrite stored events, completed attempts or existing
          receipts. These endpoints are fixed local demo destinations.
        </p>
      </div>
      {success && (
        <div className="notice success" role="status">
          {success}
        </div>
      )}
      {resource.loading ? (
        <Loading>Loading receiver configurations...</Loading>
      ) : resource.error ? (
        <ErrorBox message={resource.error} onRetry={refresh} />
      ) : resource.data?.length === 0 ? (
        <ErrorBox
          message="No demo receiver configurations were returned. Refresh to read the fixed local destinations."
          onRetry={refresh}
        />
      ) : (
        <div className="receiver-grid">
          {resource.data?.map((destination) => {
            const mode = selected[destination.id] ?? destination.mode;
            return (
              <section
                key={destination.id}
                className="sheet receiver-card"
                aria-labelledby={`receiver-${destination.id}`}
              >
                <div className="section-bar">
                  <h2 id={`receiver-${destination.id}`}>{destination.name}</h2>
                  <span className="version-label">
                    Version {destination.version}
                  </span>
                </div>
                <div className="panel-body">
                  <code className="receiver-route">{destination.route}</code>
                  <p className="current-mode">
                    Recorded mode:{" "}
                    <strong>{modeLabels[destination.mode]}</strong>
                  </p>
                  <div className="field">
                    <label htmlFor={`mode-${destination.id}`}>
                      {destination.name} mode
                    </label>
                    <select
                      id={`mode-${destination.id}`}
                      value={mode}
                      disabled={busyId !== null || locked[destination.id]}
                      onChange={(event) =>
                        setSelected((value) => ({
                          ...value,
                          [destination.id]: event.target.value as ReceiverMode,
                        }))
                      }
                    >
                      {Object.entries(modeLabels).map(([value, label]) => (
                        <option key={value} value={value}>
                          {label}
                        </option>
                      ))}
                    </select>
                    <p className="field-help">
                      {mode === "HEALTHY"
                        ? "Verify the signed request and return a receipt immediately."
                        : mode === "FAIL"
                          ? "Verify the signed request, return HTTP 503 and apply no synthetic effect."
                          : "Commit one synthetic effect, then wait 3.5 seconds. The worker times out after 2 seconds; repeated requests also wait."}
                    </p>
                  </div>
                  {errors[destination.id] && (
                    <div className="notice error" role="alert">
                      <p>{errors[destination.id]}</p>
                      {locked[destination.id] && (
                        <button className="button secondary" onClick={refresh}>
                          Refresh before continuing
                        </button>
                      )}
                    </div>
                  )}
                  <button
                    className="button primary"
                    disabled={
                      busyId !== null ||
                      locked[destination.id] ||
                      mode === destination.mode
                    }
                    onClick={() => {
                      void save(destination);
                    }}
                  >
                    {busyId === destination.id
                      ? "Saving receiver mode..."
                      : `Save ${destination.name} mode`}
                  </button>
                </div>
              </section>
            );
          })}
        </div>
      )}
      <section
        className="receiver-explanation"
        aria-labelledby="receiver-evidence-heading"
      >
        <h2 id="receiver-evidence-heading">
          Read sender and receiver evidence together
        </h2>
        <p>
          In delayed mode the local receiver records an effect before the
          worker’s deadline. A retry can therefore produce another receipt for
          the same event while the synthetic effect count stays at one. Switch
          to healthy mode and redrive a dead-letter event to inspect this
          behavior.
        </p>
        <a className="button secondary" href="#/deliveries">
          Open delivery register
        </a>
      </section>
      <LastUpdate value={resource.updatedAt} />
    </>
  );
}
