import { chromium } from "playwright";
import path from "node:path";
import fs from "node:fs";
import { fileURLToPath } from "node:url";
import { spawn } from "node:child_process";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { startJwksServer, signJWT } from "./jwt-helper.mjs";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(__dirname, "../../../..");
const EXT_DIST = path.join(REPO_ROOT, "tools/extension/dist");
const USER_DATA_DIR = path.join(__dirname, "chrome-profile-remote");
const SHOT_DIR = path.join(REPO_ROOT, ".claude/scratchpad/webmcp-e2e-remote");
// Relative to REPO_ROOT (the spawned process's cwd): pkg/config.Load passes
// this straight to viper.SetConfigName, which resolves it against
// AddConfigPath(".") — an absolute path here gets misinterpreted as a
// filename and fails to resolve.
const CONFIG_PATH = ".claude/skills/webmcp-e2e/config.remote.e2e.yaml";
const MCP_URL = "http://localhost:9999/mcp/app1";
const EDGE_WS_URL = "ws://localhost:9999/edge/ws";
const DEMO_URL = "http://localhost:5173";
const ISSUER = "https://e2e-idp.example.com";
const AUDIENCE = "manifold";
const KID = "e2e-kid";

fs.mkdirSync(SHOT_DIR, { recursive: true });
fs.rmSync(USER_DATA_DIR, { recursive: true, force: true });

function log(...args) {
  console.log(new Date().toISOString(), ...args);
}

function assertExpected(condition, message) {
  if (!condition) throw new Error(`expectation failed: ${message}`);
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// startManifold launches the gateway against CONFIG_PATH with TEST=true (so
// client.HTTPClient() permits fetching the loopback JWKS server at startup —
// see pkg/internal/client/http.go) and JWKS_URL pointed at jwksUrl.
async function startManifold(jwksUrl) {
  const child = spawn(
    "go",
    ["run", ".", "gateway", "--config", CONFIG_PATH],
    {
      cwd: REPO_ROOT,
      env: {
        ...process.env,
        ENCRYPT_KEY: Buffer.alloc(32).toString("base64"),
        JWKS_URL: jwksUrl,
        TEST: "true",
      },
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  let output = "";
  const onData = (chunk) => {
    output += chunk.toString();
  };
  child.stdout.on("data", onData);
  child.stderr.on("data", onData);

  const deadline = Date.now() + 40000;
  while (Date.now() < deadline) {
    if (output.includes('"msg":"starting server"')) return child;
    if (child.exitCode !== null) {
      throw new Error(`manifold exited early (code ${child.exitCode}):\n${output}`);
    }
    await sleep(300);
  }
  child.kill();
  throw new Error(`manifold did not report startup within timeout:\n${output}`);
}

async function newMcpClient(token) {
  const transport = new StreamableHTTPClientTransport(new URL(MCP_URL), {
    requestInit: { headers: { Authorization: `Bearer ${token}` } },
  });
  const client = new Client({ name: "webmcp-e2e-remote-client", version: "0.0.1" });
  await client.connect(transport);
  return client;
}

async function issuePairingCode(token) {
  const client = await newMcpClient(token);
  const result = await client.callTool({ name: "create_pairing_code", arguments: {} });
  await client.close();
  const text = result.content?.[0]?.text ?? "";
  const match = text.match(/Pairing code: (\d+)/);
  if (!match) throw new Error(`could not parse pairing code from: ${text}`);
  return match[1];
}

// pairIPRateLimitMax in pkg/services/edge/pairing.go — how many /edge/pair
// attempts a trusted forwarder's real IP may make within the rate-limit
// window before further attempts return 429.
const PAIR_IP_RATE_LIMIT_MAX = 10;

async function pairAttempt(headers) {
  const res = await fetch("http://localhost:9999/edge/pair", {
    method: "POST",
    headers: { "Content-Type": "application/json", ...headers },
    body: JSON.stringify({ code: "00000000" }),
  });
  return res.status;
}

async function testTrustedForwarderHeaderSpoofingIsIgnored() {
  const realIP = "203.0.113.1";
  const spoofedCFIP = "198.51.100.99";

  const statuses = [];
  for (let i = 0; i < PAIR_IP_RATE_LIMIT_MAX; i++) {
    statuses.push(await pairAttempt({ "X-Forwarded-For": realIP }));
  }
  const eleventh = await pairAttempt({ "X-Forwarded-For": realIP });
  const spoofed = await pairAttempt({
    "X-Forwarded-For": realIP,
    "CF-Connecting-IP": spoofedCFIP,
  });

  const outcome = {
    firstTenStatuses: statuses,
    eleventhExpected: 429,
    eleventhActual: eleventh,
    spoofedAttemptExpected: 429,
    spoofedAttemptActual: spoofed,
  };
  log("=== header spoofing isolation check (trustedForwarders: 127.0.0.1/32) ===");
  log(`attempts 1-${PAIR_IP_RATE_LIMIT_MAX} (X-Forwarded-For: ${realIP}) statuses:`, statuses);
  log(`attempt ${PAIR_IP_RATE_LIMIT_MAX + 1} (same IP)  expected: 429  actual: ${eleventh}`);
  log(
    `attempt ${PAIR_IP_RATE_LIMIT_MAX + 2} (X-Forwarded-For: ${realIP} + spoofed ` +
      `CF-Connecting-IP: ${spoofedCFIP})  expected: 429 (CF-Connecting-IP must be ` +
      `ignored for a trustedForwarders-origin connection)  actual: ${spoofed}`,
  );
  assertExpected(
    statuses.every((s) => s !== 429),
    `the first ${PAIR_IP_RATE_LIMIT_MAX} attempts must not be rate-limited yet, got: ${statuses}`,
  );
  assertExpected(eleventh === 429, `11th attempt should be rate-limited, got ${eleventh}`);
  assertExpected(
    spoofed === 429,
    `a spoofed CF-Connecting-IP must not bypass the trustedForwarders' ` +
      `X-Forwarded-For rate limit, got ${spoofed}`,
  );
  return outcome;
}

async function main() {
  const results = {};

  // --- identity: self-hosted JWKS + a signed JWT for sub "user-a" ---
  const jwks = await startJwksServer(KID);
  log("jwks server:", jwks.url);

  let manifold;
  let context;
  try {
    const tokenA = await signJWT({
      privateKey: jwks.privateKey,
      kid: KID,
      sub: "e2e-user-a",
      issuer: ISSUER,
      audience: AUDIENCE,
    });

    manifold = await startManifold(jwks.url);
    log("manifold started");

    context = await chromium.launchPersistentContext(USER_DATA_DIR, {
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

    // --- Step: pairing (identityKey derived from tokenA's Bearer) ---
    const code = await issuePairingCode(tokenA);
    log("pairing code:", code);

    const popup = await context.newPage();
    await popup.goto(popupUrl);
    await popup.screenshot({ path: path.join(SHOT_DIR, "01-popup-initial.png") });

    await popup.fill("#edge-url", EDGE_WS_URL);
    await popup.fill("#pairing-code", code);
    await popup.click("button[type=submit]");
    await popup.waitForTimeout(1000);
    await popup.screenshot({ path: path.join(SHOT_DIR, "02-popup-paired.png") });
    results.pairingPopupStatus = await popup.textContent("#status");
    results.pairingPopupError = await popup.textContent("#error");
    log("popup status after pairing:", results.pairingPopupStatus, "| error:", results.pairingPopupError);

    await popup.reload();
    await popup.waitForTimeout(1000);
    results.popupStatusAfterReload = await popup.textContent("#status");
    log("popup status after reload:", results.popupStatusAfterReload);
    await popup.screenshot({ path: path.join(SHOT_DIR, "03-popup-status-reload.png") });

    // --- Step: open the demo tab ---
    const demoPage = await context.newPage();
    await demoPage.goto(DEMO_URL);
    await demoPage.waitForTimeout(2000);
    await demoPage.screenshot({ path: path.join(SHOT_DIR, "04-demo-page-opened.png") });

    // --- Step: tools/list on /mcp/app1 (authenticated as tokenA) should
    // include the page's WebMCP tools, proving remote identity resolution
    // routes this Bearer to the tab HandleAppUp bound moments ago. ---
    async function listToolNames() {
      const client = await newMcpClient(tokenA);
      const tools = await client.listTools();
      await client.close();
      return tools.tools.map((t) => t.name);
    }

    let toolNames = await listToolNames();
    log("tools/list (attempt 1):", toolNames);
    for (let i = 0; i < 10 && !toolNames.includes("echo"); i++) {
      await sleep(1000);
      toolNames = await listToolNames();
      log(`tools/list (retry ${i + 2}):`, toolNames);
    }
    results.toolsListFinal = toolNames;
    for (const name of ["echo", "get_current_time", "increment_counter", "decrement_counter"]) {
      assertExpected(toolNames.includes(name), `tools/list should include "${name}", got: ${toolNames}`);
    }

    // --- Step: call echo and increment_counter as tokenA ---
    const client = await newMcpClient(tokenA);
    const echoResult = await client.callTool({
      name: "echo",
      arguments: { message: "hello from remote e2e" },
    });
    log("echo result:", JSON.stringify(echoResult));
    const echoText = echoResult.content?.[0]?.text;
    assertExpected(echoText === "hello from remote e2e", `echo mismatch, got: ${echoText}`);

    const incResult = await client.callTool({ name: "increment_counter", arguments: { by: 3 } });
    log("increment_counter result:", JSON.stringify(incResult));
    const incText = incResult.content?.[0]?.text;
    assertExpected(incText === "3", `increment_counter mismatch, got: ${incText}`);
    await client.close();

    await demoPage.waitForTimeout(500);
    await demoPage.screenshot({ path: path.join(SHOT_DIR, "05-demo-page-after-increment.png") });
    results.counterTextAfterIncrement = await demoPage.textContent("#counter-value");
    assertExpected(
      results.counterTextAfterIncrement === "3",
      `page counter mismatch, got: ${results.counterTextAfterIncrement}`,
    );

    // --- Step: a JWT for a different sub ("user-b") must NOT reach
    // user-a's bound tab — proves per-identityKey routing isolation. ---
    const tokenB = await signJWT({
      privateKey: jwks.privateKey,
      kid: KID,
      sub: "e2e-user-b",
      issuer: ISSUER,
      audience: AUDIENCE,
    });
    const clientB = await newMcpClient(tokenB);
    const toolsB = await clientB.listTools();
    results.userBToolNames = toolsB.tools.map((t) => t.name);
    log("tools/list as user-b (unpaired):", results.userBToolNames);
    await clientB.close();
    assertExpected(
      results.userBToolNames.length === 1 && results.userBToolNames[0] === "create_pairing_code",
      `an unpaired identityKey must only see create_pairing_code, got: ${results.userBToolNames}`,
    );

    // --- Step: close the tab, then call increment_counter again as tokenA ---
    await demoPage.close();
    await sleep(1000);
    {
      const c = await newMcpClient(tokenA);
      const result = await c.callTool({ name: "increment_counter", arguments: {} });
      log("increment_counter after tab close:", JSON.stringify(result));
      results.afterCloseResult = result;
      assertExpected(
        result.isError && result.content?.[0]?.text?.includes("対象アプリのタブが開かれていません。"),
        `expected tab-not-connected guidance, got: ${JSON.stringify(result)}`,
      );
      await c.close();
    }

    await popup.screenshot({ path: path.join(SHOT_DIR, "06-popup-final.png") });

    // --- Step: spoofed-header isolation for a trustedForwarders-origin
    // connection (pkg/interfaces/http/edge_pair_handler.go's resolveIP,
    // fixed per CodeRabbit review 4999741085). config.remote.e2e.yaml trusts
    // 127.0.0.1/32 and ::1/128 (this script's own connection, whichever
    // family "localhost" resolves to) as a trustedForwarders entry, which
    // only honors X-Forwarded-For — CF-Connecting-IP must be ignored for it.
    // Uses an invalid code so /edge/pair still runs RateLimitPairAttempt
    // (before code validation) without consuming a real pairing code. ---
    results.headerSpoofingCheck = await testTrustedForwarderHeaderSpoofingIsIgnored();

    fs.writeFileSync(path.join(SHOT_DIR, "results.json"), JSON.stringify(results, null, 2));

    log("done");
  } finally {
    if (context) await context.close();
    if (manifold) manifold.kill();
    await jwks.close();
  }
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
