// CDP helper for the SSO login handoff (方案 工作流 B).
//
// Connects to the running server-side headful Chromium over CDP and performs ONE
// action, then detaches (without killing the browser). Node + playwright are
// preinstalled in the ARTEX image.
//
// Usage:
//   node cdp-helper.js navigate <url>
//   node cdp-helper.js export-state <outfile>
//   node cdp-helper.js url
//
// CDP endpoint comes from env CDP_ENDPOINT (default http://127.0.0.1:9222).

const { chromium } = require("playwright");

async function main() {
  const [, , action, ...rest] = process.argv;
  const cdp = process.env.CDP_ENDPOINT || "http://127.0.0.1:9222";
  const browser = await chromium.connectOverCDP(cdp);
  try {
    const ctx = browser.contexts()[0] || (await browser.newContext());
    const page = ctx.pages()[0] || (await ctx.newPage());
    if (action === "navigate") {
      if (!rest[0]) throw new Error("navigate requires a url");
      await page.goto(rest[0], { waitUntil: "domcontentloaded", timeout: 60000 });
      console.log(JSON.stringify({ ok: true, url: page.url() }));
    } else if (action === "export-state") {
      if (!rest[0]) throw new Error("export-state requires an output path");
      const state = await ctx.storageState({ path: rest[0] });
      console.log(JSON.stringify({ ok: true, path: rest[0], cookies: (state.cookies || []).length }));
    } else if (action === "url") {
      console.log(JSON.stringify({ ok: true, url: page.url() }));
    } else {
      throw new Error("unknown action: " + action);
    }
  } finally {
    // Detaches the CDP connection; the headful browser keeps running for the operator.
    await browser.close();
  }
}

main().catch((e) => {
  console.log(JSON.stringify({ ok: false, error: String((e && e.message) || e) }));
  process.exitCode = 1;
});
