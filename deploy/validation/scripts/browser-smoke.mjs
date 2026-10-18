// Browser-level W1 smoke for the isolated edge.
//
// The edge uses a disposable self-signed certificate, so this context ignores
// HTTPS errors only for the validation origin. It asserts that the storefront
// really loads its catalog through the same-origin /api/v1 path, which host
// curl checks alone cannot prove.
//
// Usage: node deploy/validation/scripts/browser-smoke.mjs
// Requires the frontend repository's Playwright installation (FRONTEND_CONTEXT).
import { existsSync } from "node:fs";
import { createRequire } from "node:module";
import process from "node:process";

const site = process.env.VALIDATION_SITE_ORIGIN || "https://validation.localhost:58443";
const frontendContext = process.env.FRONTEND_CONTEXT
  || new URL("../../../necly-frontend/package.json", import.meta.url).pathname;
const requireFromFrontend = createRequire(
  frontendContext.endsWith("package.json") ? frontendContext : `${frontendContext}/package.json`,
);
const { chromium } = requireFromFrontend("playwright");

const failures = [];
const consoleErrors = [];
const apiResponses = [];

// Pin a full Chromium binary when the Playwright default headless shell is not
// installed. CHROMIUM_EXECUTABLE overrides both.
function chromiumExecutable() {
  if (process.env.CHROMIUM_EXECUTABLE) return process.env.CHROMIUM_EXECUTABLE;
  const candidates = [chromium.executablePath()];
  const cache = `${process.env.HOME}/.cache/ms-playwright`;
  for (let version = 1300; version >= 1000; version -= 1) {
    candidates.push(`${cache}/chromium-${version}/chrome-linux/chrome`);
  }
  return candidates.find((candidate) => existsSync(candidate)) || chromium.executablePath();
}

const browser = await chromium.launch({
  executablePath: chromiumExecutable(),
  headless: true,
  args: ["--host-resolver-rules=MAP validation.localhost 127.0.0.1"],
});
try {
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  page.on("response", (response) => {
    const url = response.url();
    if (url.includes("/api/v1/")) {
      apiResponses.push({ url, status: response.status() });
    }
  });

  const response = await page.goto(`${site}/products`, {
    waitUntil: "domcontentloaded",
    timeout: 30_000,
  });
  if (response?.status() !== 200) {
    failures.push(`/products returned ${response?.status()}`);
  }

  const heading = (await page.getByRole("heading", { level: 1 }).first().textContent()) || "";
  if (!heading.includes("Produk")) {
    failures.push(`unexpected catalog heading: ${heading.trim()}`);
  }

  // The storefront's auth bootstrap calls /api/v1/auth/refresh from the
  // browser. A 401 is the expected anonymous result and proves that the baked
  // API base is same-origin and actually reachable from the page.
  await page.waitForResponse(
    (response) => response.url().includes("/api/v1/auth/refresh"),
    { timeout: 10_000 },
  ).catch(() => {});
  const bootstrapCall = apiResponses.find(
    (entry) => entry.url.endsWith("/api/v1/auth/refresh") || entry.url.includes("/api/v1/auth/refresh?"),
  );
  if (!bootstrapCall) {
    failures.push("browser made no same-origin /api/v1/auth/refresh request");
  } else {
    if (bootstrapCall.status !== 401) {
      failures.push(`anonymous auth bootstrap returned ${bootstrapCall.status}, expected 401`);
    }
    if (new URL(bootstrapCall.url).origin !== site) {
      failures.push(`browser API origin ${new URL(bootstrapCall.url).origin} != ${site}`);
    }
  }

  // Prove the browser itself can call the public catalog through the same
  // origin (CSP connect-src, cookies, edge proxy). Product pages still render
  // static seed data until the W6 live-catalog work.
  const browserCatalog = await page.evaluate(async () => {
    const response = await fetch("/api/v1/public/products", { credentials: "include" });
    const payload = await response.json().catch(() => null);
    return {
      status: response.status,
      count: Array.isArray(payload?.products) ? payload.products.length : -1,
    };
  });
  if (browserCatalog.status !== 200 || browserCatalog.count < 1) {
    failures.push(`in-page catalog fetch returned status=${browserCatalog.status} count=${browserCatalog.count}`);
  }

  const relevantConsoleErrors = consoleErrors.filter(
    (message) => !/favicon|Failed to load resource.*401/.test(message),
  );
  if (relevantConsoleErrors.length) {
    failures.push(`browser console errors: ${relevantConsoleErrors.join(" | ")}`);
  }

  console.log(`site_origin=${site}`);
  console.log(`catalog_heading=${heading.trim()}`);
  console.log(`anonymous_bootstrap_status=${bootstrapCall ? bootstrapCall.status : "missing"}`);
  console.log(`in_page_catalog_status=${browserCatalog.status}`);
  console.log(`in_page_catalog_products=${browserCatalog.count}`);
  console.log(`api_responses=${JSON.stringify(apiResponses)}`);
  await context.close();
} finally {
  await browser.close();
}

if (failures.length) {
  console.error("FAIL: browser smoke");
  for (const failure of failures) console.error(`  - ${failure}`);
  process.exit(1);
}
console.log("PASS: browser called the API through the same-origin /api/v1 path");
