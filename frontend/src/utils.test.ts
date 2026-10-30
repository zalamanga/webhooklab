import { describe, expect, it } from "vitest";
import type { Delivery } from "./types";
import {
  attemptExplanation,
  codePointLength,
  dateTime,
  pageNumber,
  payloadBytes,
  redriveReason,
  validateNote,
  validatePayload,
} from "./utils";

function delivery(change: Partial<Delivery> = {}): Delivery {
  return {
    id: "event",
    eventType: "order.created",
    destinationId: "warehouse",
    destinationName: "Warehouse demo",
    payload: "{}",
    state: "DEAD_LETTER",
    version: 4,
    cycle: 1,
    cycleAttempts: 3,
    totalAttempts: 3,
    redrives: 0,
    nextAttemptAt: null,
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    ...change,
  };
}
describe("payload boundaries", () => {
  it("measures UTF-8 bytes rather than UTF-16 length", () => {
    expect(payloadBytes("😀")).toBe(4);
    expect(payloadBytes("é")).toBe(2);
    expect(validatePayload('{"value":"' + "😀".repeat(4096) + '"}')).toMatch(
      /16 KiB/,
    );
  });
  it("requires an object-shaped, nonempty input and leaves syntax validation to the server", () => {
    expect(validatePayload("  ")).toMatch(/JSON object/);
    expect(validatePayload("[1,2]")).toMatch(/JSON object/);
    expect(validatePayload("123")).toMatch(/JSON object/);
    expect(
      validatePayload('{"amount":9007199254740993.25,"exponent":1e300}'),
    ).toBeNull();
    expect(validatePayload("{}")).toBeNull();
  });
});
describe("bounded redrive notes and state", () => {
  it("counts trimmed Unicode code points consistently with the API", () => {
    expect(codePointLength("  😀  ")).toBe(1);
    expect(validateNote("😀".repeat(9))).toMatch(/10 and 500/);
    expect(validateNote("😀".repeat(10))).toBeNull();
    expect(validateNote("😀".repeat(500))).toBeNull();
    expect(validateNote("😀".repeat(501))).toMatch(/10 and 500/);
  });
  it("enables only dead-letter events with a remaining redrive budget", () => {
    expect(redriveReason(delivery())).toBeNull();
    expect(redriveReason(delivery({ redrives: 1, cycle: 2 }))).toBeNull();
    expect(
      redriveReason(delivery({ redrives: 2, cycle: 3, totalAttempts: 9 })),
    ).toMatch(/used both redrives/);
    expect(redriveReason(delivery({ state: "SENDING" }))).toMatch(
      /dead letter/,
    );
    expect(redriveReason(delivery({ state: "DELIVERED" }))).toMatch(
      /successful HTTP/,
    );
  });
});
describe("navigation and evidence presentation", () => {
  it("distinguishes a missing HTTP response from a recorded status with a failed body read", () => {
    expect(
      attemptExplanation({ status: "NETWORK_ERROR", httpStatus: null }),
    ).toMatch(/No confirmed HTTP/);
    expect(
      attemptExplanation({ status: "NETWORK_ERROR", httpStatus: 503 }),
    ).toMatch(/HTTP status was recorded/);
    expect(attemptExplanation({ status: "TIMEOUT", httpStatus: 503 })).toMatch(
      /response preview could not be read/,
    );
    expect(
      attemptExplanation({ status: "SUCCEEDED", httpStatus: 204 }),
    ).toMatch(/successful HTTP response/);
  });
  it("bounds page numbers to nonnegative safe integers", () => {
    expect(pageNumber(new URLSearchParams("page=2"))).toBe(2);
    for (const value of ["-1", "1.2", "NaN", "9007199254740992"])
      expect(pageNumber(new URLSearchParams({ page: value }))).toBe(0);
  });
  it("uses explicit UTC and a distinct missing timestamp", () => {
    expect(dateTime(null)).toBe("Not recorded");
    expect(dateTime("2026-01-01T00:00:00Z")).toContain("00:00:00 UTC");
  });
});
