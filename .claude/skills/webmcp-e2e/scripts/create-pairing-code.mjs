import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";

const url = new URL("http://localhost:9999/mcp/app1");

// edge.pairing.type=static では reverse サーバーに JWT ミドルウェア自体が
// 掛からないため、Authorization ヘッダーは不要
const transport = new StreamableHTTPClientTransport(url);

const client = new Client({ name: "webmcp-e2e-client", version: "0.0.1" });
await client.connect(transport);

const tools = await client.listTools();
console.log("tools:", JSON.stringify(tools, null, 2));

const result = await client.callTool({ name: "create_pairing_code", arguments: {} });
console.log("create_pairing_code result:", JSON.stringify(result, null, 2));

await client.close();
