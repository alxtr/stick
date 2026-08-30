const { test, expect } = require("@playwright/test");
const { spawn } = require("node:child_process");
const {
  createSign,
  generateKeyPairSync,
} = require("node:crypto");
const fs = require("node:fs/promises");
const http = require("node:http");
const net = require("node:net");
const os = require("node:os");
const path = require("node:path");

let application;
let stickSequence = 0;

test.beforeAll(async () => {
  application = await startApplication();
});

test.beforeEach(() => {
  application.provider.selectAccount("admin");
  application.provider.setAutoLogin(true);
});

test.afterAll(async () => {
  if (application) {
    await application.stop();
  }
});

test("authenticates and completes the main browser workflow", async ({ page }) => {
  await page.goto(application.url, { waitUntil: "networkidle" });
  await expect(page).toHaveURL(`${application.url}/`);
  await expect(page).toHaveTitle(/the grove/);
  await expect(page.getByText("admin@example.com", { exact: true })).toBeVisible();
  await expect(page.locator(".pill--admin")).toHaveText("admin");

  await createStick(page, application, uniqueStickName("main workflow"));

  await page.locator('input[name="reason"]').fill("browser smoke test");
  const claimResponsePromise = page.waitForResponse((response) =>
    response.request().method() === "POST" && response.url().endsWith("/claim"),
  );
  await page.locator("button.btn-claim").click();
  expect((await claimResponsePromise).status()).toBe(303);
  await expect(page.getByText("browser smoke test")).toBeVisible();

  const releaseResponsePromise = page.waitForResponse((response) =>
    response.request().method() === "POST" && response.url().endsWith("/release"),
  );
  await page.locator("button.btn-release").click();
  expect((await releaseResponsePromise).status()).toBe(303);
  await expect(page.locator("button.btn-claim")).toBeVisible();
  await expect(page.getByText("browser smoke test", { exact: true })).toBeVisible();
});

test("admin can rename, archive, and restore a stick", async ({ page }) => {
  await loginAs(page, application, "admin");
  const originalName = uniqueStickName("admin lifecycle");
  const renamedName = uniqueStickName("renamed");
  await createStick(page, application, originalName);

  await page.locator("#stick-name").fill(renamedName);
  await page.getByRole("button", { name: "Rename" }).click();
  await expect(page.getByRole("heading", { name: renamedName, exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Archive stick" }).click();
  await expect(page).toHaveURL(`${application.url}/`);
  await expect(page.getByRole("heading", { name: "Archived sticks" })).toBeVisible();
  await page.getByRole("link", { name: new RegExp(escapeRegExp(renamedName)) }).click();
  await expect(page.getByText("archived", { exact: true })).toBeVisible();
  await expect(page.getByText("This stick is archived and cannot be claimed.")).toBeVisible();

  await page.getByRole("button", { name: "Restore stick" }).click();
  await expect(page).toHaveURL(`${application.url}/`);
  await page.getByRole("link", { name: new RegExp(escapeRegExp(renamedName)) }).click();
  await expect(page.getByRole("button", { name: /Pick up/ })).toBeVisible();
});

test("regular users can use sticks but cannot administer them", async ({ browser, page }) => {
  await loginAs(page, application, "admin");
  const detailURL = await createStick(page, application, uniqueStickName("regular user"));
  const userContext = await browser.newContext();

  try {
    const userPage = await userContext.newPage();
    await loginAs(userPage, application, "user");
    await expect(userPage.locator(".pill--admin")).toHaveCount(0);
    await expect(userPage.getByRole("link", { name: "New stick" })).toHaveCount(0);

    const forbidden = await userPage.goto(`${application.url}/sticks/new`);
    expect(forbidden).not.toBeNull();
    expect(forbidden.status()).toBe(403);
    await expect(userPage.getByRole("heading", { name: "Not allowed" })).toBeVisible();
    await expect(userPage.locator("body")).not.toContainText("internal error");

    await userPage.goto(detailURL, { waitUntil: "networkidle" });
    await userPage.locator('input[name="reason"]').fill("regular user work");
    await userPage.getByRole("button", { name: /Pick up/ }).click();
    await expect(userPage.getByText("regular user work", { exact: true })).toBeVisible();
    await userPage.getByRole("button", { name: "Put down" }).click();
    await expect(userPage.getByRole("button", { name: /Pick up/ })).toBeVisible();
  } finally {
    await userContext.close();
  }
});

test("users can subscribe to a held stick and see it released", async ({ browser, page }) => {
  await loginAs(page, application, "admin");
  const detailURL = await createStick(page, application, uniqueStickName("notifications"));
  const holderContext = await browser.newContext();
  const watcherContext = await browser.newContext();

  try {
    const holderPage = await holderContext.newPage();
    await loginAs(holderPage, application, "user");
    await holderPage.goto(detailURL, { waitUntil: "networkidle" });
    await holderPage.locator('input[name="reason"]').fill("holder work");
    await holderPage.getByRole("button", { name: /Pick up/ }).click();

    const watcherPage = await watcherContext.newPage();
    await loginAs(watcherPage, application, "watcher");
    await watcherPage.goto(detailURL, { waitUntil: "networkidle" });
    await expect(watcherPage.getByText("in use", { exact: true })).toBeVisible();
    await expect(watcherPage.getByText("user@example.com", { exact: true })).toBeVisible();
    await watcherPage.getByRole("button", { name: "Notify me" }).click();
    await expect(watcherPage.getByRole("button", { name: "Cancel notification" })).toBeVisible();
    await watcherPage.getByRole("button", { name: "Cancel notification" }).click();
    await expect(watcherPage.getByRole("button", { name: "Notify me" })).toBeVisible();

    await holderPage.getByRole("button", { name: "Put down" }).click();
    await watcherPage.reload({ waitUntil: "networkidle" });
    await expect(watcherPage.getByRole("button", { name: /Pick up/ })).toBeVisible();
  } finally {
    await holderContext.close();
    await watcherContext.close();
  }
});

test("stale browser forms show a conflict instead of overwriting changes", async ({ browser, page }) => {
  await loginAs(page, application, "admin");
  const originalName = uniqueStickName("stale form");
  const renamedName = uniqueStickName("fresh name");
  const detailURL = await createStick(page, application, originalName);
  const staleContext = await browser.newContext();

  try {
    const stalePage = await staleContext.newPage();
    await loginAs(stalePage, application, "admin");
    await stalePage.goto(detailURL, { waitUntil: "networkidle" });
    const staleVersion = await stalePage.locator('form[action$="/rename"] input[name="version"]').inputValue();

    await page.locator("#stick-name").fill(renamedName);
    await page.getByRole("button", { name: "Rename" }).click();
    await expect(page.getByRole("heading", { name: renamedName, exact: true })).toBeVisible();

    await stalePage.locator("#stick-name").fill(uniqueStickName("stale name"));
    const conflictResponsePromise = stalePage.waitForResponse((response) =>
      response.request().method() === "POST" && response.url().endsWith("/rename"),
    );
    await stalePage.getByRole("button", { name: "Rename" }).click();
    const conflictResponse = await conflictResponsePromise;
    expect(conflictResponse.status()).toBe(409);
    expect(new URL(stalePage.url()).search).toBe("");
    await expect(stalePage.getByText(/This stick changed since the page was loaded/)).toBeVisible();
    await expect(stalePage.getByRole("heading", { name: renamedName, exact: true })).toBeVisible();
    await expect(stalePage.locator("#stick-name")).toHaveValue(renamedName);
    const freshVersion = await stalePage.locator('form[action$="/rename"] input[name="version"]').inputValue();
    expect(freshVersion).not.toBe(staleVersion);
  } finally {
    await staleContext.close();
  }
});

test("invalid browser forms display validation errors", async ({ page }) => {
  await loginAs(page, application, "admin");
  await page.getByRole("link", { name: "New stick" }).click();
  await page.getByLabel("Stick name").fill("invalid/name");
  const invalidNameResponsePromise = page.waitForResponse((response) =>
    response.request().method() === "POST" && response.url().endsWith("/sticks/new"),
  );
  await page.getByRole("button", { name: /Create stick/ }).click();
  const invalidNameResponse = await invalidNameResponsePromise;
  expect(invalidNameResponse.status()).toBe(422);
  expect(new URL(page.url()).search).toBe("");
  await expect(page.getByLabel("Stick name")).toHaveValue("invalid/name");
  await expect(page.getByLabel("Stick name")).toHaveAttribute("aria-invalid", "true");
  await expect(page.getByText(/only letters, digits, hyphens, and spaces/)).toBeVisible();

  await page.goto(`${application.url}/`, { waitUntil: "networkidle" });
  await createStick(page, application, uniqueStickName("invalid reason"));
  await page.locator('input[name="reason"]').fill("   ");
  const invalidReasonResponsePromise = page.waitForResponse((response) =>
    response.request().method() === "POST" && response.url().endsWith("/claim"),
  );
  await page.getByRole("button", { name: /Pick up/ }).click();
  const invalidReasonResponse = await invalidReasonResponsePromise;
  expect(invalidReasonResponse.status()).toBe(422);
  expect(new URL(page.url()).search).toBe("");
  await expect(page.locator('input[name="reason"]')).toHaveValue("   ");
  await expect(page.locator('input[name="reason"]')).toHaveAttribute("aria-invalid", "true");
  await expect(page.getByText("Reason must be non-empty and at most 500 characters.", { exact: true })).toBeVisible();
});

test("logout clears the browser session", async ({ page }) => {
  await loginAs(page, application, "admin");
  const logoutResponsePromise = page.waitForResponse((response) =>
    response.url() === `${application.url}/auth/logout`,
  );
  await page.getByRole("button", { name: "logout" }).click();
  const logoutResponse = await logoutResponsePromise;
  expect(logoutResponse.status()).toBe(302);
  expect(logoutResponse.headers().location).toBe("/auth/login");
  const cookies = await page.context().cookies();
  expect(cookies.some((cookie) => cookie.name === "stick_session")).toBe(false);
});

test("mounted applications keep browser navigation and assets under the mount path", async ({ browser }) => {
  const mountedApplication = await startApplication({ mountPath: "/stick" });
  const mountedPage = await browser.newPage();
  const failedRequests = [];
  const consoleErrors = [];
  mountedPage.on("requestfailed", (request) => failedRequests.push(request.url()));
  mountedPage.on("console", (message) => {
    if (message.type() === "error") {
      consoleErrors.push(message.text());
    }
  });

  try {
    await loginAs(mountedPage, mountedApplication, "admin");
    await expect(mountedPage.locator('link[rel="stylesheet"]')).toHaveAttribute(
      "href",
      "/stick/assets/styles.css",
    );
    await expect(mountedPage.getByRole("link", { name: "New stick" })).toHaveAttribute(
      "href",
      "/stick/sticks/new",
    );
    expect(failedRequests).toEqual([]);
    expect(consoleErrors).toEqual([]);

    await mountedPage.getByRole("link", { name: "New stick" }).click();
    await mountedPage.getByLabel("Stick name").fill("invalid/name");
    const mountedValidationPromise = mountedPage.waitForResponse((response) =>
      response.request().method() === "POST" && response.url().endsWith("/stick/sticks/new"),
    );
    await mountedPage.getByRole("button", { name: /Create stick/ }).click();
    const mountedValidation = await mountedValidationPromise;
    expect(mountedValidation.status()).toBe(422);
    expect(new URL(mountedPage.url()).search).toBe("");
    await expect(mountedPage.locator('link[rel="stylesheet"]')).toHaveAttribute("href", "/stick/assets/styles.css");
    await expect(mountedPage.getByLabel("Stick name")).toHaveValue("invalid/name");

    const unmountedResponse = await mountedPage.goto(mountedApplication.origin);
    expect(unmountedResponse).not.toBeNull();
    expect(unmountedResponse.status()).toBe(404);
  } finally {
    await mountedApplication.stop();
  }
});

async function startApplication({ mountPath = "" } = {}) {
  const root = path.resolve(__dirname, "../..");
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), "stick-browser-"));
  const provider = await startOIDCProvider();
  const port = await freePort();
  const origin = `http://127.0.0.1:${port}`;
  const url = `${origin}${mountPath}`;
  const configPath = path.join(directory, "config.yaml");
  const config = [
    `database: ${yamlString(path.join(directory, "stick.db"))}`,
    "server:",
    `  public_url: ${yamlString(url)}`,
    `  listen_addr: ${yamlString(`127.0.0.1:${port}`)}`,
    "auth:",
    "  oidc:",
    `    issuer: ${yamlString(provider.url)}`,
    "    client_id: browser-client",
    "    client_secret: browser-secret",
    "  session_secret: 0123456789abcdef0123456789abcdef",
    "  admin_emails:",
    "    - admin@example.com",
    "notifications:",
    "  webhook:",
    `    url: ${yamlString(`${provider.url}/webhook`)}`,
    "",
  ].join("\n");
  await fs.writeFile(configPath, config, { mode: 0o600 });

  const output = [];
  const child = spawn("go", ["run", "./cmd/stickd", "-config", configPath], {
    cwd: root,
    env: { ...process.env, GOWORK: "off" },
    stdio: ["ignore", "pipe", "pipe"],
  });
  child.stdout.on("data", (chunk) => output.push(chunk.toString()));
  child.stderr.on("data", (chunk) => output.push(chunk.toString()));

  try {
    await waitForHealth(url, child);
  } catch (error) {
    await stopProcess(child);
    await provider.stop();
    throw new Error(`${error.message}\napplication output:\n${output.join("")}`);
  }

  return {
    origin,
    url,
    provider,
    stop: async () => {
      await stopProcess(child);
      await provider.stop();
      await fs.rm(directory, { recursive: true, force: true });
    },
  };
}

async function startOIDCProvider() {
  const { privateKey, publicKey } = generateKeyPairSync("rsa", {
    modulusLength: 2048,
    privateKeyEncoding: { format: "pem", type: "pkcs8" },
    publicKeyEncoding: { format: "jwk" },
  });
  const accounts = {
    admin: {
      sub: "browser-admin",
      name: "Browser Admin",
      email: "admin@example.com",
      email_verified: true,
    },
    user: {
      sub: "browser-user",
      name: "Browser User",
      email: "user@example.com",
      email_verified: true,
    },
    watcher: {
      sub: "browser-watcher",
      name: "Browser Watcher",
      email: "watcher@example.com",
      email_verified: true,
    },
  };
  let selectedAccount = "admin";
  let autoLogin = true;
  const pendingIdentities = new Map();
  const server = http.createServer(async (request, response) => {
    const requestURL = new URL(request.url, providerURL(server));
    if (requestURL.pathname === "/.well-known/openid-configuration") {
      return sendJSON(response, {
        issuer: providerURL(server),
        authorization_endpoint: `${providerURL(server)}/authorize`,
        token_endpoint: `${providerURL(server)}/token`,
        jwks_uri: `${providerURL(server)}/keys`,
      });
    }
    if (requestURL.pathname === "/authorize") {
      const code = `browser-code-${requestURL.searchParams.get("state")}`;
      pendingIdentities.set(code, accounts[selectedAccount]);
      if (!autoLogin) {
        return sendHTML(response, "<h1>Fake OIDC sign-in</h1>");
      }
      const redirect = new URL(requestURL.searchParams.get("redirect_uri"));
      redirect.searchParams.set("code", code);
      redirect.searchParams.set("state", requestURL.searchParams.get("state"));
      response.writeHead(302, { Location: redirect.toString() });
      response.end();
      return;
    }
    if (requestURL.pathname === "/token") {
      const values = new URLSearchParams(await readRequest(request));
      const code = values.get("code");
      const identity = pendingIdentities.get(code) || accounts[selectedAccount];
      pendingIdentities.delete(code);
      return sendJSON(response, {
        access_token: "browser-access-token",
        token_type: "Bearer",
        expires_in: 600,
        id_token: signIDToken(privateKey, providerURL(server), identity),
      });
    }
    if (requestURL.pathname === "/keys") {
      return sendJSON(response, {
        keys: [{ ...publicKey, kid: "browser-e2e", alg: "RS256", use: "sig" }],
      });
    }
    if (requestURL.pathname === "/webhook") {
      await readRequest(request);
      response.writeHead(204);
      response.end();
      return;
    }
    response.writeHead(404);
    response.end();
  });
  await listen(server);
  return {
    url: providerURL(server),
    selectAccount(account) {
      if (!accounts[account]) {
        throw new Error(`unknown fake OIDC account: ${account}`);
      }
      selectedAccount = account;
    },
    setAutoLogin(enabled) {
      autoLogin = enabled;
    },
    stop: () => close(server),
  };
}

function signIDToken(privateKey, issuer, identity) {
  const header = { alg: "RS256", kid: "browser-e2e", typ: "JWT" };
  const now = Math.floor(Date.now() / 1000);
  const payload = {
    iss: issuer,
    sub: identity.sub,
    aud: "browser-client",
    exp: now + 600,
    iat: now,
    name: identity.name,
    email: identity.email,
    email_verified: identity.email_verified,
  };
  const encodedHeader = base64url(JSON.stringify(header));
  const encodedPayload = base64url(JSON.stringify(payload));
  const input = `${encodedHeader}.${encodedPayload}`;
  const signer = createSign("RSA-SHA256");
  signer.update(input);
  signer.end();
  return `${input}.${signer.sign(privateKey).toString("base64url")}`;
}

async function waitForHealth(url, child) {
  const deadline = Date.now() + 90000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`stickd exited with code ${child.exitCode}`);
    }
    try {
      const response = await fetch(`${url}/healthz`);
      if (response.status === 200) {
        return;
      }
    } catch {
      // The binary may still be compiling or starting.
    }
    await delay(100);
  }
  throw new Error("stickd did not become healthy");
}

async function stopProcess(child) {
  if (child.exitCode !== null) {
    return;
  }
  child.kill("SIGINT");
  await new Promise((resolve) => {
    const timer = setTimeout(() => {
      child.kill("SIGKILL");
      resolve();
    }, 10000);
    child.once("exit", () => {
      clearTimeout(timer);
      resolve();
    });
  });
}

function providerURL(server) {
  const address = server.address();
  return `http://127.0.0.1:${address.port}`;
}

function sendJSON(response, body) {
  response.writeHead(200, { "Content-Type": "application/json" });
  response.end(JSON.stringify(body));
}

function readRequest(request) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    request.on("data", (chunk) => chunks.push(chunk));
    request.on("end", () => resolve(Buffer.concat(chunks).toString()));
    request.on("error", reject);
  });
}

function sendHTML(response, body) {
  response.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
  response.end(body);
}

function listen(server) {
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
}

function close(server) {
  return new Promise((resolve) => server.close(resolve));
}

async function loginAs(page, application, account) {
  application.provider.selectAccount(account);
  await page.goto(`${application.url}/auth/login`, { waitUntil: "networkidle" });
  await expect(page).toHaveURL(`${application.url}/`);
}

async function createStick(page, application, name) {
  await page.getByRole("link", { name: "New stick" }).click();
  await expect(page).toHaveURL(`${application.url}/sticks/new`);
  await page.getByLabel("Stick name").fill(name);
  const createResponsePromise = page.waitForResponse((response) =>
    response.request().method() === "POST" && response.url().endsWith("/sticks/new"),
  );
  await page.getByRole("button", { name: /Create stick/ }).click();
  const createResponse = await createResponsePromise;
  expect(createResponse.status()).toBe(303);
  const applicationPath = new URL(application.url).pathname.replace(/\/$/, "");
  expect(createResponse.headers().location).toBe(`${applicationPath}/`);
  await expect(page).toHaveURL(`${application.url}/`);
  await page.getByRole("link", { name: new RegExp(escapeRegExp(name)) }).click();
  await expect(page.getByRole("heading", { name, exact: true })).toBeVisible();
  return page.url();
}

function uniqueStickName(label) {
  stickSequence += 1;
  return `Browser ${label} ${process.pid} ${stickSequence}`;
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function freePort() {
  const server = net.createServer();
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const port = server.address().port;
      server.close(() => resolve(port));
    });
  });
}

function yamlString(value) {
  return JSON.stringify(value);
}

function base64url(value) {
  return Buffer.from(value).toString("base64url");
}

function delay(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}
