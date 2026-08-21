const INITIAL_DELAY_MS = 1000;
const MAX_DELAY_MS = 30000;

/**
 * Exponential backoff (1s -> 30s cap) with half-jitter, per
 * docs/design/webmcp-reverse-gateway.ja.md ("再接続"). `attempt` is 0-based
 * (0 = first reconnect try after the initial connection drops).
 */
export function computeReconnectDelayMs(attempt: number, random: () => number = Math.random): number {
  const base = Math.min(INITIAL_DELAY_MS * 2 ** attempt, MAX_DELAY_MS);
  return base / 2 + random() * (base / 2);
}
