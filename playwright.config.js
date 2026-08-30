const { defineConfig } = require("@playwright/test");

module.exports = defineConfig({
  testDir: "./test/browser",
  timeout: 120000,
  expect: {
    timeout: 10000,
  },
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? "line" : "list",
  use: {
    browserName: "chromium",
    headless: true,
    javaScriptEnabled: false,
    trace: "retain-on-failure",
  },
});
