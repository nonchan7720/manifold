# A2A agent example

Exposes an [A2A (Agent2Agent)](https://a2a-protocol.org/) agent through Manifold as an MCP server, next to any `mcpServers` you already have.

- Manifold fetches the agent's **Agent Card** from `agents.<name>.url` (`/.well-known/agent-card.json`; both the v0.3 and v1.0 card formats are accepted).
- Every **skill** in the card becomes one MCP tool named after the skill id.
- A `tools/call` sends an A2A `message/send` to the endpoint the card declares, with the caller's `sessionId` as the A2A `contextId`.
- The response context comes back in `_meta.a2a` (`contextId`, `taskId`, `state`, …).

## Run

You need an A2A agent to talk to. Any agent that publishes an Agent Card works; for a quick local one, run one of the [a2a-go examples](https://github.com/a2aproject/a2a-go/tree/main/examples) or the [a2a-samples](https://github.com/a2aproject/a2a-samples) and point `TRANSLATOR_AGENT_URL` at it.

```bash
cd examples/a2a-agent
# Generate once and reuse — see ../README.md
export ENCRYPT_KEY=${ENCRYPT_KEY:-$(openssl rand -base64 32)}
export TRANSLATOR_AGENT_URL=http://localhost:10000
manifold gateway
```

## Try it

```bash
# The agent is listed like any server; ?tools=true lists its skills
curl http://localhost:9999/mcp/list?tools=true
```

Connect from Claude Code and call a skill:

```bash
claude mcp add --transport http translator http://localhost:9999/mcp/translator
```

A call looks like this (tool name = skill id):

```json
{
  "name": "translate",
  "arguments": {
    "sessionId": "session-123",
    "message": "Translate this to Japanese: good morning",
    "data": { "targetLanguage": "ja" },
    "files": [
      { "url": "https://example.com/doc.txt" },
      { "base64": "aGVsbG8=", "filename": "hello.txt", "contentType": "text/plain" }
    ]
  }
}
```

and the result carries the A2A context:

```json
{
  "content": [{ "type": "text", "text": "おはようございます" }],
  "_meta": {
    "a2a": {
      "protocolVersion": "1.0",
      "contextId": "session-123",
      "taskId": "task-42",
      "state": "completed"
    }
  }
}
```

Authentication (`authValue` / `oauth2` / `tokenExchange`), extra headers and [OPA tool authorization](../opa/) work exactly as for `mcpServers`; with authz, the policy sees `server=translator` and `tool=<skill id>`.
