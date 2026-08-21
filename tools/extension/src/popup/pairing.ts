const WS_TO_HTTP_SCHEME: Record<string, string> = {
  "ws:": "http:",
  "wss:": "https:",
};

function edgeHttpOrigin(edgeUrl: string): string {
  const parsed = new URL(edgeUrl);
  const httpScheme = WS_TO_HTTP_SCHEME[parsed.protocol] ?? parsed.protocol;
  return `${httpScheme}//${parsed.host}`;
}

/**
 * Exchanges a short-lived pairing code for a long-lived edge token via
 * POST {edgeURL origin}/edge/pair, per
 * docs/design/webmcp-reverse-gateway.ja.md ("pairing モード").
 */
export async function exchangePairingCode(
  edgeUrl: string,
  code: string,
  fetchImpl: typeof fetch = fetch,
): Promise<string> {
  const response = await fetchImpl(`${edgeHttpOrigin(edgeUrl)}/edge/pair`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code }),
  });

  if (!response.ok) {
    throw new Error(`pairing failed with status ${response.status}`);
  }

  const data = (await response.json()) as { token?: string };
  if (!data.token) {
    throw new Error("pairing response did not include an edge token");
  }
  return data.token;
}
