// Run with Node's test runner and Playwright on the module path; see README.md.
// The Codex reset alert: Settings → Notifications has the row, its On and Off
// saved with the settings, and a reset the backend says is new comes up in a
// dialog once — the announcement's words, the post it came from, the site's
// credit — closed without a trace. English and Chinese; no backend, the API
// is faked here.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");

function settingsPayload(lang, noResetAlert) {
  return {
    theme: "system", lang, tray: "panel", quotaLeft: false, currency: "usd", noResetAlert,
    dock: false, dockWindow: false, proxy: "", redact: false, redactPersonal: false, redactWords: [],
    codexWarmup: "", claudeWarmup: "", codexWarmAt: "", claudeWarmAt: "", workbuddyCheckin: false, noStats: false,
    trayUsage: "", trayUsageEvery: 3, vision: "", imageGen: "",
    version: "0.1.400", dir: "~/.config/magpie", gateway: "http://127.0.0.1:3425",
    proxyNow: "none", proxySource: "none", login: false,
    visionModels: [], imageGenModels: [], workbuddyCheckins: [], lanURLs: [],
    fx: { rate: 7.2, at: new Date().toISOString(), stale: false },
  };
}

// reset: what GET /api/resetnews says, or none; dev: whether the page is a
// dev build's, which alone has the Test button
function server(lang, reset, asked, posted, dev) {
  return async (route) => {
    const req = route.request(), url = new URL(req.url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = ${JSON.stringify({ lang, theme: "system", web: false, dev: !!dev })};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/resetnews") {
      asked.push("GET /api/resetnews");
      return json(reset || { show: false });
    }
    if (url.pathname === "/api/resetnews/test") {
      posted.push("test");
      return json(reset ? reset.reset : null);
    }
    if (url.pathname === "/api/resetnews/seen") {
      posted.push("seen");
      return json({});
    }
    if (url.pathname === "/api/settings") {
      if (req.method() === "POST") posted.push(req.postDataJSON());
      return json(settingsPayload(lang, false));
    }
    if (url.pathname === "/api/state") return json({ agents: [], profiles: [], settings: { lang, theme: "system" } });
    if (url.pathname === "/api/groups") return json({ groups: [], models: [] });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
}

const reset = {
  show: true,
  reset: {
    id: "2106131810921136451", type: "regular", announced: "2026-10-02T21:18:48.000Z",
    text: "Reset all propagated. Enjoy. `magpie plugin off` too.", url: "https://x.com/thsottiaux/status/2106131810921136451",
  },
};

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  test(engine + ": the Codex reset alert", async (t) => {
    assert(["chromium", "webkit"].includes(engine), "BROWSER must be chromium or webkit");
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
    const pages = [];
    t.after(async () => {
      if (process.env.ARTIFACT_DIR) {
        await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
        for (const [i, p] of pages.filter((p) => !p.isClosed()).entries()) await p.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-resetnews-${i}.png`) });
      }
      await browser.close();
    });

    for (const [lang, tab, row, title, view, from, on, off, close, testLabel] of [
      ["en", "Notifications", "Codex reset alerts", "Codex reset", "View announcement", "Data from Codex Resets", "On", "Off", "Close", "Test"],
      ["zh", "通知", "Codex 重置提醒", "Codex 重置", "查看公告", "数据来自 Codex Resets", "开启", "关闭", "关闭", "测试"],
    ]) {
      await t.test(lang + ": a new reset announced once", async () => {
        const errors = [], asked = [], posted = [];
        const context = await browser.newContext({ viewport: { width: 900, height: 420 }, reducedMotion: "reduce" });
        const page = await context.newPage();
        pages.push(page);
        page.setDefaultTimeout(5000);
        page.on("pageerror", (e) => errors.push(e.message));
        await page.route("**/*", server(lang, reset, asked, posted));
        await page.goto("http://magpie.test/");

        // the dialog comes up by itself, the announcement in it
        const modal = page.locator("#modal .whatsnew");
        await modal.locator("b", { hasText: title }).waitFor();
        const meta = await modal.locator(".wn-ver").textContent();
        assert.equal(meta.startsWith(lang === "en" ? "Regular reset · " : "定期重置 · "), true, "the kind and when: " + meta);
        assert.equal(await modal.locator("code").textContent(), "magpie plugin off");
        assert.equal(await modal.locator("a", { hasText: view }).count(), 1, "the post is linked");
        assert.equal(await modal.locator("a", { hasText: from }).count(), 1, "the site credited");
        await modal.locator("button", { hasText: close }).click();
        await page.waitForFunction(() => document.querySelector("#modal").hidden);
        assert.deepEqual(posted, ["seen"], "shown once");
        assert.deepEqual(errors, []);
        await context.close();
      });

      await t.test(lang + ": none waiting, and Settings' row", async () => {
        const errors = [], asked = [], posted = [];
        const context = await browser.newContext({ viewport: { width: 900, height: 420 }, reducedMotion: "reduce" });
        const page = await context.newPage();
        pages.push(page);
        page.setDefaultTimeout(5000);
        page.on("pageerror", (e) => errors.push(e.message));
        await page.route("**/*", server(lang, null, asked, posted));
        await page.goto("http://magpie.test/");
        await page.waitForTimeout(300); // the ask comes and comes back empty
        assert.equal(await page.locator("#modal .whatsnew").count(), 0, "nothing announced");
        assert.ok(asked.includes("GET /api/resetnews"), "asked once at load");

        // the Notifications tab, before About, has the row, on to start
        await page.locator("#prefs").click();
        await page.locator("#setTab-notify").click();
        const segs = page.locator("#setPage-notify .segs .opt");
        assert.deepEqual(await segs.allTextContents(), [off, on]);
        const aboutOrder = await page.locator("#setTabs button").allTextContents();
        assert.ok(aboutOrder.indexOf(tab) < aboutOrder.indexOf(lang === "en" ? "About" : "关于"), "before About");
        // Off, then On: the alert saved with the settings each way
        await segs.nth(0).click();
        await page.waitForTimeout(200);
        await segs.nth(1).click();
        await page.waitForTimeout(200);
        const saves = posted.filter((p) => p && p.noResetAlert !== undefined);
        assert.deepEqual(saves.map((p) => p.noResetAlert), [true, false]);
        // a shipped app has no Test button
        assert.equal(await page.locator("#setPage-notify button", { hasText: testLabel }).count(), 0, "no Test button off dev");
        assert.deepEqual(errors, []);
        await context.close();
      });

      await t.test(lang + ": dev, the Test button asks at once", async () => {
        const errors = [], asked = [], posted = [];
        const context = await browser.newContext({ viewport: { width: 900, height: 420 }, reducedMotion: "reduce" });
        const page = await context.newPage();
        pages.push(page);
        page.setDefaultTimeout(5000);
        page.on("pageerror", (e) => errors.push(e.message));
        await page.route("**/*", server(lang, { show: false, reset: reset.reset }, asked, posted, true));
        await page.goto("http://magpie.test/");
        await page.locator("#prefs").click();
        await page.locator("#setTab-notify").click();
        // the Test button beside the toggle asks the site at once, and what
        // it has comes up as the dialog would
        const tryBtn = page.locator("#setPage-notify button", { hasText: testLabel });
        assert.equal(await tryBtn.count(), 1, "a Test button on dev");
        await tryBtn.click();
        const modal = page.locator("#modal .whatsnew");
        await modal.locator("b", { hasText: title }).waitFor();
        assert.equal(await modal.locator("code").textContent(), "magpie plugin off");
        await modal.locator("button", { hasText: close }).click();
        await page.waitForFunction(() => document.querySelector("#modal").hidden);
        assert.deepEqual(posted, ["test"], "asked, not announced");
        assert.deepEqual(errors, []);
        await context.close();
      });
    }
  });
}
