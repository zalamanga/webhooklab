import type {
  Attempt,
  AttemptStatus,
  Delivery,
  ReceiverMode,
  State,
} from "./types";

export const stateLabels: Record<State, string> = {
  QUEUED: "Queued",
  SENDING: "Sending",
  RETRY_WAIT: "Retry waiting",
  DELIVERED: "Delivered",
  DEAD_LETTER: "Dead letter",
};
export const attemptLabels: Record<AttemptStatus, string> = {
  STARTED: "Started",
  SUCCEEDED: "Succeeded",
  HTTP_ERROR: "HTTP error",
  TIMEOUT: "Timeout",
  NETWORK_ERROR: "Network error",
  ABANDONED: "Abandoned",
};
export const modeLabels: Record<ReceiverMode, string> = {
  HEALTHY: "Healthy",
  FAIL: "Return 503",
  TIMEOUT: "Apply, then delay",
};
export function attemptExplanation(
  attempt: Pick<Attempt, "status" | "httpStatus">,
): string {
  if (
    attempt.httpStatus !== null &&
    ["NETWORK_ERROR", "TIMEOUT"].includes(attempt.status)
  ) {
    return "The HTTP status was recorded, but the response preview could not be read. Remote processing remains uncertain.";
  }
  if (attempt.status === "TIMEOUT")
    return "The sender timed out after its request deadline. Remote processing may have happened.";
  if (attempt.status === "ABANDONED")
    return "The lease expired before a result was recorded. Remote processing is unknown.";
  if (attempt.status === "NETWORK_ERROR")
    return "No confirmed HTTP response was recorded. This attempt does not prove the remote outcome.";
  if (attempt.status === "STARTED")
    return "A worker claim was recorded. No completed result is recorded yet.";
  if (attempt.status === "HTTP_ERROR")
    return "The receiver returned a non-success HTTP status.";
  return "The sender recorded a successful HTTP response.";
}
export function dateTime(value: string | null): string {
  if (!value) return "Not recorded";
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return value;
  return (
    new Intl.DateTimeFormat("en-GB", {
      day: "2-digit",
      month: "short",
      year: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      timeZone: "UTC",
    }).format(date) + " UTC"
  );
}
export function actionLabel(value: string) {
  return value
    .replace(/[_.]/g, " ")
    .toLowerCase()
    .replace(/^./, (letter) => letter.toUpperCase());
}
export function codePointLength(value: string) {
  return Array.from(value.trim()).length;
}
export function validateNote(value: string): string | null {
  const count = codePointLength(value);
  return count < 10 || count > 500
    ? "Write a redrive note between 10 and 500 characters."
    : null;
}
export function payloadBytes(value: string) {
  return new TextEncoder().encode(value).byteLength;
}
export function validatePayload(value: string): string | null {
  if (!value.trim()) return "Enter a JSON object as the event payload.";
  if (payloadBytes(value) > 16 * 1024)
    return "The payload must be 16 KiB or smaller in UTF-8.";
  if (!value.trim().startsWith("{") || !value.trim().endsWith("}"))
    return "The payload must be a JSON object.";
  return null;
}
export function redriveReason(delivery: Delivery): string | null {
  if (delivery.redrives >= 2)
    return "This event has used both redrives. Its complete attempt and receipt evidence remains available.";
  if (delivery.state === "DELIVERED")
    return "The sender recorded a successful HTTP response. This event does not need a redrive.";
  if (delivery.state !== "DEAD_LETTER")
    return "Redrive is available after an event reaches dead letter.";
  return null;
}
export function pageNumber(query: URLSearchParams) {
  const page = Number(query.get("page") || 0);
  return Number.isSafeInteger(page) && page >= 0 ? page : 0;
}
