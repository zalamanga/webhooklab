import type { ReactNode } from "react";
import type { AttemptStatus, AuditItem, Page, State } from "./types";
import { actionLabel, attemptLabels, dateTime, stateLabels } from "./utils";

export function Loading({
  children = "Loading workspace...",
}: {
  children?: ReactNode;
}) {
  return (
    <div className="state-box" role="status">
      <span className="loading-rule" aria-hidden="true" />
      {children}
    </div>
  );
}
export function ErrorBox({
  message,
  onRetry,
}: {
  message: string;
  onRetry?: () => void;
}) {
  return (
    <div className="notice error" role="alert">
      <div>
        <strong>Request not completed</strong>
        <p>{message}</p>
      </div>
      {onRetry && (
        <button className="button secondary" onClick={onRetry}>
          Try again
        </button>
      )}
    </div>
  );
}
export function Empty({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <div className="empty-state">
      <h2>{title}</h2>
      <div className="muted">{children}</div>
    </div>
  );
}
export function Heading({
  title,
  description,
  action,
}: {
  title: string;
  description?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="page-heading">
      <div>
        <h1>{title}</h1>
        {description && <div className="page-description">{description}</div>}
      </div>
      {action && <div className="heading-action">{action}</div>}
    </div>
  );
}
export function StateLabel({ state }: { state: State }) {
  return (
    <span className={`state-label state-${state.toLowerCase()}`}>
      {stateLabels[state]}
    </span>
  );
}
export function AttemptLabel({ status }: { status: AttemptStatus }) {
  return (
    <span className={`attempt-label attempt-${status.toLowerCase()}`}>
      {attemptLabels[status]}
    </span>
  );
}
export function LastUpdate({
  value,
  polling = false,
}: {
  value: string | null;
  polling?: boolean;
}) {
  return (
    <p className="last-update">
      {value
        ? `Last response ${dateTime(value)}`
        : "Waiting for a server response."}
      {polling && (
        <span>Checks every 2 seconds while this view is visible.</span>
      )}
    </p>
  );
}
export function Pagination({
  page,
  label,
  onChange,
}: {
  page: Page<unknown>;
  label: string;
  onChange: (page: number) => void;
}) {
  if (page.total === 0) return null;
  const pages = Math.max(1, Math.ceil(page.total / page.pageSize));
  return (
    <nav className="pagination" aria-label={`${label} pages`}>
      <p>
        {page.page * page.pageSize + 1} to{" "}
        {Math.min((page.page + 1) * page.pageSize, page.total)} of {page.total}{" "}
        {label}
      </p>
      <div className="pagination-controls">
        <button
          className="button secondary"
          disabled={page.page === 0}
          onClick={() => onChange(page.page - 1)}
        >
          Previous
        </button>
        <span>
          Page {page.page + 1} of {pages}
        </span>
        <button
          className="button secondary"
          disabled={page.page + 1 >= pages}
          onClick={() => onChange(page.page + 1)}
        >
          Next
        </button>
      </div>
    </nav>
  );
}
export function AuditTable({ items }: { items: AuditItem[] }) {
  return (
    <div
      className="table-scroll"
      role="region"
      aria-label="Audit events"
      tabIndex={0}
    >
      <table className="audit-table">
        <thead>
          <tr>
            <th scope="col">Time / actor</th>
            <th scope="col">Action</th>
            <th scope="col">Event</th>
            <th scope="col">Note</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => (
            <tr key={item.id}>
              <td>
                <time dateTime={item.createdAt}>
                  {dateTime(item.createdAt)}
                </time>
                <span className="row-meta">{item.actor}</span>
              </td>
              <td>{actionLabel(item.action)}</td>
              <td>
                {item.eventId ? (
                  <a
                    className="row-link event-id"
                    href={`#/events/${item.eventId}`}
                  >
                    {item.eventId}
                  </a>
                ) : (
                  <span className="muted">Workspace control</span>
                )}
              </td>
              <td className="audit-note">{item.note || "No note recorded."}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
