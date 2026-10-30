import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";
import type { Delivery, DeliveryDetail, ReceiverMode } from "../src/types";

const password = process.env.WEBHOOK_DEMO_PASSWORD;
test.skip(
  !password,
  "Set WEBHOOK_DEMO_PASSWORD to the generated local password.",
);
const payload =
  '{"orderId":"synthetic-browser-order","preciseAmount":9007199254740993.25}';
async function login(page: Page) {
  await page.goto("/");
  await page
    .getByLabel("Email address", { exact: true })
    .fill("operator@webhooklab.test");
  await page.getByLabel("Password", { exact: true }).fill(password!);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("navigation", { name: "Workspace", exact: true }),
  ).toBeVisible();
}
async function navigate(page: Page, name: string) {
  await page
    .getByRole("navigation", { name: "Workspace", exact: true })
    .getByRole("link", { name, exact: true })
    .click();
}
async function setMode(
  page: Page,
  destination: "Warehouse demo" | "Billing demo",
  mode: ReceiverMode,
) {
  await navigate(page, "Demo receivers");
  const field = page.getByLabel(`${destination} mode`, { exact: true });
  await expect(field).toBeEnabled();
  await field.selectOption(mode);
  const save = page.getByRole("button", {
    name: `Save ${destination} mode`,
    exact: true,
  });
  if (await save.isEnabled()) {
    await save.click();
    await expect(
      page
        .locator(".notice.success")
        .filter({ hasText: `${destination} now uses` }),
    ).toBeVisible();
  }
}
async function setPaused(page: Page, paused: boolean) {
  await navigate(page, "Deliveries");
  const action = page.getByRole("button", {
    name: /^(Pause|Resume) new claims$/,
  });
  await expect(action).toBeEnabled();
  const currentlyPaused = await page
    .getByRole("button", { name: "Resume new claims", exact: true })
    .isVisible();
  if (currentlyPaused !== paused) await action.click();
  await expect(
    page.getByText(paused ? "New claims paused" : "New claims enabled", {
      exact: true,
    }),
  ).toBeVisible();
}
async function compose(page: Page, destination = "warehouse") {
  await navigate(page, "Send event");
  await page
    .getByLabel("Destination", { exact: true })
    .selectOption(destination);
  await page
    .getByLabel("Event type", { exact: true })
    .selectOption("order.created");
  await page.getByLabel("JSON payload", { exact: true }).fill(payload);
  await page
    .getByRole("button", { name: "Record and queue event", exact: true })
    .click();
}
async function readEvent(page: Page, id: string): Promise<DeliveryDetail> {
  const response = await page.request.get(`/api/events/${id}`);
  expect(response.ok()).toBe(true);
  return response.json() as Promise<DeliveryDetail>;
}
function eventId(page: Page) {
  return new URL(page.url()).hash.split("/").pop()!;
}

test("pause retains a queued event, resume delivers it and preserves precise JSON text", async ({
  page,
}) => {
  await login(page);
  await setMode(page, "Warehouse demo", "HEALTHY");
  await setPaused(page, true);
  await expect(page.locator(".worker-evidence")).toContainText(
    /[1-9]\d* workers? seen within the last 15 seconds/,
  );
  await compose(page);
  await expect(page.locator(".heading-action .state-label")).toHaveText(
    "Queued",
  );
  const id = eventId(page);
  await page.waitForResponse(
    (response) =>
      response.request().method() === "GET" &&
      new URL(response.url()).pathname === `/api/events/${id}`,
  );
  await expect(page.locator(".attempt-record")).toHaveCount(0);
  await setPaused(page, false);
  await page.goto(`/#/events/${id}`);
  await expect(page.locator(".heading-action .state-label")).toHaveText(
    "Delivered",
  );
  const detail = await readEvent(page, id);
  expect(detail.delivery.payload).toBe(payload);
  expect(detail.attempts).toHaveLength(1);
  expect(detail.receipt?.effectCount).toBe(1);
  await page.getByText("Event payload", { exact: true }).click();
  await page.getByText("Event payload", { exact: true }).click();
  await expect(
    page.getByLabel("Immutable JSON payload", { exact: true }),
  ).toContainText("9007199254740993.25");
  await page.locator(".response-details summary").first().click();
  await expect(page.locator(".response-details pre").first()).toBeVisible();
});

test("real timeout leaves unknown attempts but one local effect, then healthy redrive deduplicates", async ({
  page,
}) => {
  await login(page);
  await setPaused(page, false);
  await setMode(page, "Warehouse demo", "TIMEOUT");
  await compose(page);
  await expect(page.locator(".heading-action .state-label")).toHaveText(
    "Dead letter",
    { timeout: 35_000 },
  );
  const id = eventId(page);
  await expect(
    page.getByText("An attempt has an unknown remote outcome.", {
      exact: true,
    }),
  ).toBeVisible();
  const failed = await readEvent(page, id);
  expect(failed.attempts).toHaveLength(3);
  expect(failed.attempts.every((attempt) => attempt.status === "TIMEOUT")).toBe(
    true,
  );
  expect(failed.receipt?.effectCount).toBe(1);
  expect(failed.receipt?.receiptCount).toBe(3);
  await expect(
    page.locator(".receipt-counts > div").first().locator("strong"),
  ).toHaveText("1");
  await setMode(page, "Warehouse demo", "HEALTHY");
  await page.goto(`/#/events/${id}`);
  await page
    .getByLabel("Redrive note", { exact: true })
    .fill("Receiver is healthy again. Retry the same immutable event.");
  await page
    .getByRole("button", { name: "Queue a new cycle", exact: true })
    .click();
  await expect(page.locator(".heading-action .state-label")).toHaveText(
    "Delivered",
  );
  const completed = await readEvent(page, id);
  expect(completed.delivery.redrives).toBe(1);
  expect(completed.delivery.totalAttempts).toBe(4);
  expect(completed.receipt?.effectCount).toBe(1);
  expect(completed.receipt?.receiptCount).toBe(4);
  expect(
    completed.attempts.filter((attempt) => attempt.status === "TIMEOUT"),
  ).toHaveLength(3);
});

test("real HTTP failures consume all three bounded cycles without a receiver effect", async ({
  page,
}) => {
  await login(page);
  await setPaused(page, false);
  await setMode(page, "Billing demo", "FAIL");
  await compose(page, "billing");
  const idPromise = page.waitForURL(/#\/events\/[0-9a-f-]{36}$/);
  await idPromise;
  const id = eventId(page);
  for (let redrive = 0; redrive <= 2; redrive += 1) {
    await expect(page.locator(".heading-action .state-label")).toHaveText(
      "Dead letter",
      { timeout: 25_000 },
    );
    const detail = await readEvent(page, id);
    expect(detail.delivery.totalAttempts).toBe((redrive + 1) * 3);
    expect(detail.receipt).toBeNull();
    expect(
      detail.attempts.every(
        (attempt) =>
          attempt.status === "HTTP_ERROR" && attempt.httpStatus === 503,
      ),
    ).toBe(true);
    if (redrive < 2) {
      await page
        .getByLabel("Redrive note", { exact: true })
        .fill(
          `Controlled failure cycle ${redrive + 2}: verify the bounded retry budget.`,
        );
      await page
        .getByRole("button", { name: "Queue a new cycle", exact: true })
        .click();
      await expect(
        page
          .locator(".notice.success")
          .filter({ hasText: "Redrive recorded." }),
      ).toBeVisible();
    }
  }
  await expect(
    page.getByText(
      "This event has used both redrives. Its complete attempt and receipt evidence remains available.",
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Queue a new cycle", exact: true }),
  ).toHaveCount(0);
  await setMode(page, "Billing demo", "HEALTHY");
});

test("a committed response loss retains exact identity across session expiry and retries as HTTP 200", async ({
  page,
  context,
}) => {
  await login(page);
  await setPaused(page, false);
  await setMode(page, "Warehouse demo", "HEALTHY");
  const identities: string[] = [];
  let committed: Delivery | null = null;
  let dropResponse = true;
  await page.route("**/api/events", async (route) => {
    if (route.request().method() !== "POST") {
      await route.continue();
      return;
    }
    identities.push(route.request().headers()["idempotency-key"]);
    if (dropResponse) {
      dropResponse = false;
      const response = await route.fetch({
        postData: route.request().postDataBuffer()!,
        headers: await route.request().allHeaders(),
      });
      if (response.status() !== 201) {
        const error = (await response.json().catch(() => ({}))) as {
          code?: string;
          message?: string;
        };
        throw new Error(
          `Real create returned ${response.status()}: ${error.code || "UNKNOWN"}: ${error.message || "No JSON error message"}`,
        );
      }
      committed = (await response.json()) as Delivery;
      await route.abort("failed");
    } else await route.continue();
  });
  await compose(page);
  await expect(
    page.getByText("Creation outcome is not confirmed", { exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("JSON payload", { exact: true })).toBeDisabled();
  await page.getByText("Request identity", { exact: true }).click();
  await expect(page.locator(".request-details code")).toHaveText(identities[0]);
  await context.clearCookies();
  await page
    .getByRole("button", { name: "Retry the same event", exact: true })
    .click();
  await expect(
    page.getByText("Your session expired", { exact: true }),
  ).toBeVisible();
  await page.getByLabel("Password", { exact: true }).fill(password!);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByLabel("JSON payload", { exact: true })).toBeDisabled();
  const replay = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      new URL(response.url()).pathname === "/api/events",
  );
  await page
    .getByRole("button", { name: "Retry the same event", exact: true })
    .click();
  expect((await replay).status()).toBe(200);
  await expect(page.locator(".detail-id")).toHaveText(
    (committed as Delivery | null)!.id,
  );
  expect(identities).toHaveLength(3);
  expect(new Set(identities).size).toBe(1);
});

test("mobile keyboard, receiver version conflict, filters and audit remain usable", async ({
  page,
  context,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await page.getByLabel("Email address", { exact: true }).focus();
  await page.keyboard.press("Tab");
  await expect(page.getByLabel("Password", { exact: true })).toBeFocused();
  await page.getByLabel("Password", { exact: true }).fill(password!);
  await page.keyboard.press("Tab");
  await page.keyboard.press("Enter");
  await expect(
    page.getByRole("navigation", { name: "Workspace", exact: true }),
  ).toBeVisible();
  await setMode(page, "Warehouse demo", "HEALTHY");
  const stale = await context.newPage();
  await stale.goto("/#/receivers");
  await expect(
    stale.getByLabel("Warehouse demo mode", { exact: true }),
  ).toBeEnabled();
  await page
    .getByLabel("Warehouse demo mode", { exact: true })
    .selectOption("FAIL");
  await page
    .getByRole("button", { name: "Save Warehouse demo mode", exact: true })
    .click();
  await expect(
    page
      .locator(".notice.success")
      .filter({ hasText: "Warehouse demo now uses" }),
  ).toBeVisible();
  await stale
    .getByLabel("Warehouse demo mode", { exact: true })
    .selectOption("TIMEOUT");
  await stale
    .getByRole("button", { name: "Save Warehouse demo mode", exact: true })
    .click();
  await expect(
    stale.getByText(
      "This receiver configuration changed. Refresh receiver modes before editing again.",
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    stale.getByLabel("Warehouse demo mode", { exact: true }),
  ).toBeDisabled();
  await stale
    .getByRole("button", { name: "Refresh before continuing", exact: true })
    .click();
  await expect(
    stale.getByLabel("Warehouse demo mode", { exact: true }),
  ).toHaveValue("FAIL");
  await stale.close();
  await setMode(page, "Warehouse demo", "HEALTHY");
  const createKeys: string[] = [];
  page.on("request", (request) => {
    if (
      request.method() === "POST" &&
      new URL(request.url()).pathname === "/api/events"
    )
      createKeys.push(request.headers()["idempotency-key"]);
  });
  const before = (await (
    await page.request.get("/api/events?page=0&state=ALL&search=")
  ).json()) as { total: number };
  await navigate(page, "Send event");
  await page.getByLabel("JSON payload", { exact: true }).fill('{"broken": }');
  const rejected = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      new URL(response.url()).pathname === "/api/events",
  );
  await page
    .getByRole("button", { name: "Record and queue event", exact: true })
    .click();
  expect((await rejected).status()).toBe(400);
  await expect(
    page.getByText("Event could not be created", { exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("JSON payload", { exact: true })).toBeEnabled();
  const after = (await (
    await page.request.get("/api/events?page=0&state=ALL&search=")
  ).json()) as { total: number };
  expect(after.total).toBe(before.total);
  await page.getByLabel("JSON payload", { exact: true }).fill(payload);
  await page
    .getByRole("button", { name: "Record and queue event", exact: true })
    .click();
  await expect(page.locator(".heading-action .state-label")).toHaveText(
    "Delivered",
  );
  expect(createKeys).toHaveLength(2);
  expect(createKeys[0]).not.toBe(createKeys[1]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await navigate(page, "Deliveries");
  await page
    .getByLabel("Event ID or type", { exact: true })
    .fill("no-such-synthetic-event");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(
    page.getByRole("heading", {
      name: "No events match these filters",
      exact: true,
    }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Show all events", exact: true })
    .click();
  await page
    .getByLabel("Delivery state", { exact: true })
    .selectOption("DEAD_LETTER");
  await page
    .getByRole("button", { name: "Clear filters", exact: true })
    .click();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await navigate(page, "Audit");
  await expect(page.getByRole("table")).toBeVisible();
  await page
    .getByRole("button", { name: "Refresh history", exact: true })
    .click();
  await expect(page.getByRole("table")).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Sign in to WebhookLab", exact: true }),
  ).toBeVisible();
});
