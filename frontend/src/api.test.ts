import { afterEach, describe, expect, it, vi } from "vitest";
import {
  api,
  ApiError,
  creationFailureStatus,
  setCsrfToken,
  uncertainResult,
} from "./api";

afterEach(() => {
  vi.unstubAllGlobals();
  setCsrfToken("");
});
describe("authenticated transport", () => {
  it("retains exact payload text and caller identity in an authenticated JSON mutation", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(new Response('{"id":"event"}', { status: 201 }));
    vi.stubGlobal("fetch", fetchMock);
    setCsrfToken("synthetic-csrf-token");
    const payload = '{"amount":9007199254740993.25,"exponent":1e300}';
    await api("/events", {
      method: "POST",
      headers: { "Idempotency-Key": "retained-request-identity" },
      body: JSON.stringify({
        destinationId: "warehouse",
        eventType: "order.created",
        payload,
      }),
    });
    const [url, options] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/events");
    expect(options.credentials).toBe("same-origin");
    expect(options.cache).toBe("no-store");
    expect(new Headers(options.headers).get("Content-Type")).toBe(
      "application/json",
    );
    expect(new Headers(options.headers).get("X-CSRF-Token")).toBe(
      "synthetic-csrf-token",
    );
    expect(new Headers(options.headers).get("Idempotency-Key")).toBe(
      "retained-request-identity",
    );
    expect(JSON.parse(options.body as string).payload).toBe(payload);
  });
  it("announces session expiry and retains safe structured errors", async () => {
    const events = new EventTarget();
    const expired = vi.fn();
    events.addEventListener("webhooklab:session-expired", expired);
    vi.stubGlobal("window", events);
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          new Response(
            '{"code":"UNAUTHENTICATED","message":"Sign in again."}',
            { status: 401 },
          ),
        ),
    );
    await expect(api("/dashboard")).rejects.toMatchObject({
      status: 401,
      code: "UNAUTHENTICATED",
      message: "Sign in again.",
    });
    expect(expired).toHaveBeenCalledOnce();
  });
  it("does not announce an expected logged-out session check", async () => {
    const events = new EventTarget();
    const expired = vi.fn();
    events.addEventListener("webhooklab:session-expired", expired);
    vi.stubGlobal("window", events);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response("{}", { status: 401 })),
    );
    await expect(api("/auth/session", {}, false)).rejects.toBeInstanceOf(
      ApiError,
    );
    expect(expired).not.toHaveBeenCalled();
  });
  it("distinguishes unknown network/server outcomes from definite input rejection", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new TypeError("Failed to fetch")),
    );
    await expect(api("/events")).rejects.toMatchObject({
      status: 0,
      code: "NETWORK_ERROR",
    });
    expect(uncertainResult(new ApiError(0, "Network"))).toBe(true);
    expect(uncertainResult(new ApiError(503, "Unavailable"))).toBe(true);
    expect(uncertainResult(new ApiError(400, "Invalid payload"))).toBe(false);
    expect(uncertainResult(new ApiError(409, "Version conflict"))).toBe(false);
  });
  it("accepts a successful empty logout response", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response(null, { status: 204 })),
    );
    await expect(
      api("/auth/logout", { method: "POST" }),
    ).resolves.toBeUndefined();
  });
  it("keeps a possibly committed creation locked after a later retry expires or is rejected", () => {
    expect(
      creationFailureStatus("idle", new ApiError(400, "Invalid payload")),
    ).toBe("failed");
    expect(
      creationFailureStatus("idle", new ApiError(0, "Dropped response")),
    ).toBe("uncertain");
    expect(
      creationFailureStatus("uncertain", new ApiError(401, "Session expired")),
    ).toBe("uncertain");
    expect(
      creationFailureStatus("uncertain", new ApiError(400, "Rejected retry")),
    ).toBe("uncertain");
    expect(
      creationFailureStatus(
        "idle",
        new ApiError(409, "Identity or capacity conflict"),
      ),
    ).toBe("conflict");
    expect(
      creationFailureStatus("conflict", new ApiError(403, "CSRF check failed")),
    ).toBe("uncertain");
  });
});
