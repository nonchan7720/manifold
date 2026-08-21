// @mcp-b/global polyfills document.modelContext for browsers without a
// native WebMCP implementation yet; it's a no-op bridge otherwise, per
// docs/design/webmcp-reverse-gateway.ja.md ("document.modelContext が正").
import "@mcp-b/global";

const counterValueEl = document.querySelector<HTMLElement>("#counter-value");
let counter = 0;

function renderCounter(): void {
  if (counterValueEl) counterValueEl.textContent = String(counter);
}

document.modelContext.registerTool({
  name: "echo",
  description: "Echoes back the given message.",
  inputSchema: {
    type: "object",
    properties: {
      message: { type: "string", description: "Text to echo back" },
    },
    required: ["message"],
  },
  async execute(args: { message: string }) {
    return { content: [{ type: "text", text: args.message }] };
  },
});

document.modelContext.registerTool({
  name: "get_current_time",
  description: "Returns the current time as an ISO-8601 string.",
  inputSchema: { type: "object", properties: {} },
  async execute() {
    return { content: [{ type: "text", text: new Date().toISOString() }] };
  },
});

document.modelContext.registerTool({
  name: "increment_counter",
  description: "Increments the on-page counter and returns its new value.",
  inputSchema: {
    type: "object",
    properties: {
      by: { type: "integer", description: "Amount to add (default 1)" },
    },
  },
  async execute(args: { by?: number }) {
    counter += args.by ?? 1;
    renderCounter();
    return { content: [{ type: "text", text: String(counter) }] };
  },
});

document.modelContext.registerTool({
  name: "decrement_counter",
  description: "Decrements the on-page counter and returns its new value.",
  inputSchema: {
    type: "object",
    properties: {
      by: { type: "integer", description: "Amount to subtract (default 1)" },
    },
  },
  async execute(args: { by?: number }) {
    counter -= args.by ?? 1;
    renderCounter();
    return { content: [{ type: "text", text: String(counter) }] };
  },
});
