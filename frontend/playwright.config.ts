import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  workers: 1,
  timeout: 90_000,
  expect: { timeout: 20_000 },
  use: {
    baseURL: process.env.WEBHOOK_BASE_URL || "http://127.0.0.1:8097",
    channel:
      process.env.WEBHOOK_BROWSER_CHANNEL ||
      (process.env.CI ? "chromium" : "msedge"),
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  reporter: [["list"], ["html", { open: "never" }]],
});
