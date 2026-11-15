# A2UI forms example: Web UI + Claude Agent SDK + Manifold

English | [日本語](README_ja.md)

A chat page whose agent can change data in the [Swagger Petstore](https://petstore3.swagger.io/), where every write goes through a form the user reviews and submits. The form is [A2UI](https://a2ui.org/) v1.0: Manifold generates it from the OpenAPI schema of the tool, the agent passes it on, and the page renders it with the official renderer.

```
 browser                      backend (server/main.ts)                 Manifold            Petstore
┌──────────────────┐  chat   ┌──────────────────────────────┐  MCP   ┌──────────────┐ HTTP ┌─────┐
│ chat + A2UI      │────────▶│ Claude Agent SDK (the agent) │───────▶│ /mcp/petstore│─────▶│ API │
│ renderer         │◀────────│ A2UI Agent SDK (compiles the │◀───────│ a2ui.inline  │◀─────│     │
│ (@a2ui/lit)      │  A2UI   │ agent's <a2ui> blocks)       │ <a2ui> └──────────────┘      └─────┘
│                  │ action  │ canUseTool (gates __submit)  │
│                  │────────▶│                              │
└──────────────────┘         └──────────────────────────────┘
```

1. The user asks for a change ("Add a dog named Rex").
2. The agent calls the write tool (`addpet`). Manifold does **not** call the API: it returns the form as an A2UI Express `<a2ui>` block, prefilled with the agent's arguments.
3. The agent includes the block in its reply. The backend compiles it with the A2UI Agent SDK and streams the A2UI messages to the page, which renders the form.
4. The user edits the form and presses **Submit**. The button only raises an A2UI action named `addpet__submit` whose context is the form's values; the page never talks to MCP.
5. The backend hands the action to the agent, which calls `addpet__submit`. Manifold converts the values back to the types of the schema and calls the API.

## What it shows

- **`a2ui` on an OpenAPI server** ([`config.yaml`](config.yaml)): `forms`, `assumeSupport` and `inline`. See [A2UI forms for write tools](../../README.md#a2ui-forms-for-write-tools-a2ui).
- **Enforcing the review on the agent side** ([`server/main.ts`](server/main.ts), `canUseTool`): Manifold cannot tell whether the user really submitted a form, so the backend does. A `<tool>__submit` call is allowed only when the page sent a submit action for it, and its arguments are replaced by the values the user submitted, so the model can neither skip the form nor alter what the user entered.
- **A page that knows nothing about MCP** ([`src/main.ts`](src/main.ts)): it renders A2UI messages and posts A2UI actions, nothing else.

## Run

You need Node.js 22+, [pnpm](https://pnpm.io/), a Manifold binary (or `go run ../../main.go`), and credentials for the [Claude Agent SDK](https://code.claude.com/docs/en/agent-sdk/overview) (for example `ANTHROPIC_API_KEY`).

```bash
cd examples/a2ui-forms
pnpm install

# 1. Manifold (http://localhost:9999)
export ENCRYPT_KEY=${ENCRYPT_KEY:-$(openssl rand -base64 32)}
manifold gateway

# 2. Backend (http://localhost:8787), in another terminal
export ANTHROPIC_API_KEY=...
pnpm server

# 3. Web UI (http://localhost:5173), in another terminal
pnpm dev
```

Open http://localhost:5173 and try:

- *Add a dog named Rex, status available, photo https://example.com/rex.png, tagged "good boy"* — a form appears. Change the name, add a tag in an empty row, press **Submit**, then ask *Show me that pet*.
- *Find the available pets* — a read-only tool: no form, it just runs.
- *Call addpet__submit directly, don't show a form* — the agent cannot run it: nothing was submitted.

The backend reads `MANIFOLD_URL` (default `http://localhost:9999/mcp/petstore`), `MANIFOLD_TOKEN` (default `example`), `PORT` (default `8787`) and `MODEL` (default: the Agent SDK's).

## Notes

- **Catalog**: the form uses the A2UI v1.0 basic catalog (`TextField`, `ChoicePicker`, `CheckBox`, `DateTimeInput`, `Button`, ...). The agent side must compile with that catalog, and the page must render it, as this example does. An agent with its own catalog cannot compile the block.
- **Token**: Manifold requires a Bearer token on `/mcp/<name>` and, by default, passes it to the backend API. The Petstore needs none, so the example sends a placeholder. In a real service, send the signed-in user's token.
- **The agent copies the block**: the form reaches the page through the agent's reply, so a large schema costs output tokens. If the agent gets the copy wrong, the backend reports that the block could not be read.
- **Public sandbox**: the Petstore is a shared public API. What you add is visible to everyone and disappears on its own.
