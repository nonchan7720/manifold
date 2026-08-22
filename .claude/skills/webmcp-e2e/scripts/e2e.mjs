import { chromium } from "playwright";
import path from "node:path";
import fs from "node:fs";
import { fileURLToPath } from "node:url";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(__dirname, "../../../..");
const EXT_DIST = path.join(REPO_ROOT, "tools/extension/dist");
const USER_DATA_DIR = path.join(__dirname, "chrome-profile");
const SHOT_DIR = path.join(REPO_ROOT, ".claude/scratchpad/webmcp-e2e");
const MCP_URL = "http://localhost:9999/mcp/app1";
const EDGE_WS_URL = "ws://localhost:9999/edge/ws";
const DEMO_URL = "http://localhost:5173";

fs.mkdirSync(SHOT_DIR, { recursive: true });
fs.rmSync(USER_DATA_DIR, { recursive: true, force: true });

function log(...args) {
  console.log(new Date().toISOString(), ...args);
}

// See the 検証項目/期待結果 table in SKILL.md — a mismatch here means the
// reverse gateway itself is broken, not just this script's happy path.
function assertExpected(condition, message) {
  if (!condition) throw new Error(`expectation failed: ${message}`);
}

async function newMcpClient() {
  // edge.pairing.type=static では reverse サーバーに JWT ミドルウェア自体が
  // 掛からないため、Authorization ヘッダーは不要
  const transport = new StreamableHTTPClientTransport(new URL(MCP_URL));
  const client = new Client({ name: "webmcp-e2e-client", version: "0.0.1" });
  await client.connect(transport);
  return client;
}

async function issuePairingCode() {
  const client = await newMcpClient();
  const result = await client.callTool({ name: "create_pairing_code", arguments: {} });
  await client.close();
  const text = result.content?.[0]?.text ?? "";
  const match = text.match(/Pairing code: (\d+)/);
  if (!match) throw new Error(`could not parse pairing code from: ${text}`);
  return match[1];
}

async function sleep(ms) {
  await new Promise((resolve) => setTimeout(resolve, ms));
}

async function main() {
  const results = {};

  const context = await chromium.launchPersistentContext(USER_DATA_DIR, {
    headless: false,
    // Manifest V3 拡張は headless で正しく動かないため headless: false は必須だが、
    // ウィンドウを画面外に出してフォーカスを奪わないようにする(スクリーンショットは
    // オフスクリーンでも正しく撮れる)。
    args: [
      `--disable-extensions-except=${EXT_DIST}`,
      `--load-extension=${EXT_DIST}`,
      "--window-position=-2400,-2400",
    ],
  });

  let sw = context.serviceWorkers()[0];
  if (!sw) {
    sw = await context.waitForEvent("serviceworker", { timeout: 15000 });
  }
  const extensionId = sw.url().split("/")[2];
  log("extension id:", extensionId);

  const popupUrl = `chrome-extension://${extensionId}/src/popup/popup.html`;

  // --- Step: pairing ---
  const code = await issuePairingCode();
  log("pairing code:", code);

  const popup = await context.newPage();
  await popup.goto(popupUrl);
  await popup.screenshot({ path: path.join(SHOT_DIR, "01-popup-initial.png") });

  await popup.fill("#edge-url", EDGE_WS_URL);
  await popup.fill("#pairing-code", code);
  await popup.click("button[type=submit]");
  await popup.waitForTimeout(1000);
  await popup.screenshot({ path: path.join(SHOT_DIR, "02-popup-paired.png") });
  const statusAfterPair = await popup.textContent("#status");
  const errorAfterPair = await popup.textContent("#error");
  log("popup status after pairing:", statusAfterPair, "| error:", errorAfterPair);
  results.pairingPopupStatus = statusAfterPair;
  results.pairingPopupError = errorAfterPair;

  // Reload popup to re-query background connection status.
  await popup.reload();
  await popup.waitForTimeout(1000);
  const statusReloaded = await popup.textContent("#status");
  log("popup status after reload:", statusReloaded);
  results.popupStatusAfterReload = statusReloaded;
  await popup.screenshot({ path: path.join(SHOT_DIR, "03-popup-status-reload.png") });

  // --- Step: open the demo tab ---
  const demoPage = await context.newPage();
  await demoPage.goto(DEMO_URL);
  await demoPage.waitForTimeout(2000);
  await demoPage.screenshot({ path: path.join(SHOT_DIR, "04-demo-page-opened.png") });

  // --- Step: tools/list on /mcp/app1 should include the page's WebMCP tools ---
  async function listToolNames() {
    const client = await newMcpClient();
    const tools = await client.listTools();
    await client.close();
    return tools.tools.map((t) => t.name);
  }

  let toolNames = await listToolNames();
  log("tools/list (attempt 1):", toolNames);
  results.toolsListAttempt1 = toolNames;

  // The design's app.up handshake is asynchronous; retry briefly if the
  // page's tools haven't shown up yet.
  for (let i = 0; i < 10 && !toolNames.includes("echo"); i++) {
    await sleep(1000);
    toolNames = await listToolNames();
    log(`tools/list (retry ${i + 2}):`, toolNames);
  }
  results.toolsListFinal = toolNames;
  for (const name of ["echo", "get_current_time", "increment_counter", "decrement_counter"]) {
    assertExpected(toolNames.includes(name), `tools/list should include "${name}", got: ${toolNames}`);
  }

  // --- Step: call echo and increment_counter ---
  const client = await newMcpClient();
  const echoResult = await client.callTool({
    name: "echo",
    arguments: { message: "hello from e2e" },
  });
  log("echo result:", JSON.stringify(echoResult));
  results.echoResult = echoResult;
  const echoText = echoResult.content?.[0]?.text;
  assertExpected(echoText === "hello from e2e", `echo should return the input string, got: ${echoText}`);

  const incResult = await client.callTool({
    name: "increment_counter",
    arguments: { by: 3 },
  });
  log("increment_counter result:", JSON.stringify(incResult));
  results.incrementResult = incResult;
  const incText = incResult.content?.[0]?.text;
  assertExpected(incText === "3", `increment_counter(by: 3) should return "3", got: ${incText}`);
  await client.close();

  await demoPage.waitForTimeout(500);
  await demoPage.screenshot({ path: path.join(SHOT_DIR, "05-demo-page-after-increment.png") });
  results.counterTextAfterIncrement = await demoPage.textContent("#counter-value");
  log("counter text on page:", results.counterTextAfterIncrement);
  assertExpected(
    results.counterTextAfterIncrement === "3",
    `page counter display should match the tool result, got: ${results.counterTextAfterIncrement}`,
  );

  // --- Step: close the tab, then call increment_counter again ---
  await demoPage.close();
  await sleep(1000);
  {
    const client = await newMcpClient();
    let threw = false;
    try {
      const result = await client.callTool({ name: "increment_counter", arguments: {} });
      log("increment_counter after tab close:", JSON.stringify(result));
      results.afterCloseResult = result;
      assertExpected(
        result.isError && result.content?.[0]?.text?.includes("対象アプリのタブが開かれていません。"),
        `tool call after tab close should report isError with the tab-not-connected guidance, got: ${JSON.stringify(result)}`,
      );
    } catch (error) {
      threw = true;
      log("increment_counter after tab close threw:", error.message);
      results.afterCloseError = error.message;
    } finally {
      await client.close();
    }
    assertExpected(!threw, "increment_counter after tab close should return isError, not throw");
  }

  await popup.screenshot({ path: path.join(SHOT_DIR, "06-popup-final.png") });

  fs.writeFileSync(
    path.join(SHOT_DIR, "results.json"),
    JSON.stringify(results, null, 2),
  );

  await context.close();
  log("done");
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
