// Automated credentialed login helper (方案 增强项).
//
// Reads a JSON job on stdin, drives a headless Chromium through the configured
// login steps, verifies success, and saves the authenticated storageState.
// Independent of the noVNC/CDP stack. Node + playwright are preinstalled.
//
// stdin JSON:
//   { login_url, steps:[{action,selector,value}], state_path,
//     success_url_contains?, success_selector? }
// steps actions: goto | fill | click | press | wait
//   ($username/$password/$totp are already substituted by the caller)

const { chromium } = require("playwright");

function readStdin() {
  return new Promise((resolve) => {
    let d = "";
    process.stdin.on("data", (c) => (d += c));
    process.stdin.on("end", () => resolve(d));
  });
}

async function main() {
  const job = JSON.parse((await readStdin()) || "{}");
  const browser = await chromium.launch({ args: ["--no-sandbox", "--disable-dev-shm-usage"] });
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  try {
    if (job.login_url) await page.goto(job.login_url, { waitUntil: "domcontentloaded", timeout: 60000 });
    for (const st of job.steps || []) {
      if (st.action === "goto") await page.goto(st.value, { waitUntil: "domcontentloaded", timeout: 60000 });
      else if (st.action === "fill") await page.fill(st.selector, st.value ?? "");
      else if (st.action === "click") await page.click(st.selector);
      else if (st.action === "press") await page.keyboard.press(st.value || "Enter");
      else if (st.action === "wait") await page.waitForTimeout(Number(st.value) || 1000);
    }
    await page.waitForLoadState("networkidle", { timeout: 15000 }).catch(() => {});

    // success verification (best-effort)
    let ok = true;
    let reason = "";
    if (job.success_url_contains) {
      ok = page.url().includes(job.success_url_contains);
      if (!ok) reason = `url ${page.url()} missing ${job.success_url_contains}`;
    }
    if (ok && job.success_selector) {
      ok = (await page.$(job.success_selector)) !== null;
      if (!ok) reason = `selector ${job.success_selector} not found`;
    }
    if (!ok) throw new Error("login verification failed: " + reason);

    await ctx.storageState({ path: job.state_path });
    const st = await ctx.storageState();
    console.log(JSON.stringify({ ok: true, url: page.url(), cookies: (st.cookies || []).length }));
  } finally {
    await browser.close();
  }
}

main().catch((e) => {
  console.log(JSON.stringify({ ok: false, error: String((e && e.message) || e) }));
  process.exitCode = 1;
});
