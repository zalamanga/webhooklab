export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  constructor(status: number, message: string, code = "REQUEST_FAILED") {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

let csrfToken = "";
export function setCsrfToken(value: string) {
  csrfToken = value;
}

export async function api<T>(
  path: string,
  options: RequestInit = {},
  announceExpiry = true,
): Promise<T> {
  const headers = new Headers(options.headers);
  if (options.body) headers.set("Content-Type", "application/json");
  if (options.method && options.method !== "GET")
    headers.set("X-CSRF-Token", csrfToken);
  let response: Response;
  try {
    response = await fetch(`/api${path}`, {
      cache: "no-store",
      ...options,
      headers,
      credentials: "same-origin",
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError")
      throw error;
    throw new ApiError(
      0,
      "The server could not be reached. Check the connection and try again.",
      "NETWORK_ERROR",
    );
  }
  if (!response.ok) {
    const body = (await response.json().catch(() => ({}))) as {
      code?: string;
      message?: string;
    };
    if (response.status === 401 && announceExpiry)
      window.dispatchEvent(new Event("webhooklab:session-expired"));
    throw new ApiError(
      response.status,
      body.message || `The request failed (${response.status}).`,
      body.code,
    );
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

export function errorMessage(error: unknown) {
  return error instanceof Error
    ? error.message
    : "The request could not be completed. Try again.";
}
export function uncertainResult(error: unknown) {
  return (
    !(error instanceof ApiError) || error.status === 0 || error.status >= 500
  );
}
export type CreationStatus =
  | "idle"
  | "pending"
  | "failed"
  | "uncertain"
  | "conflict";
export function creationFailureStatus(
  previous: CreationStatus,
  error: unknown,
): CreationStatus {
  if (error instanceof ApiError && error.status === 409) return "conflict";
  if (
    uncertainResult(error) ||
    previous === "uncertain" ||
    previous === "conflict"
  )
    return "uncertain";
  return "failed";
}
