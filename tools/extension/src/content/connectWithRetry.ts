import { computeReconnectDelayMs } from "../shared/backoff";
import type { ReadyAwareTransport } from "../shared/types";

const DEFAULT_MAX_WAIT_MS = 30000;

function defaultWait(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export interface ConnectWithRetryDeps<T extends ReadyAwareTransport> {
  /** Builds a fresh transport for each attempt; the previous attempt's instance is closed first. */
  createTransport: () => T;
  /** Gives up and rejects once this much time has elapsed across all attempts. Default 30s. */
  maxWaitMs?: number;
  /** Per-attempt timeout; injectable so tests don't depend on real timers. Default: real setTimeout. */
  wait?: (ms: number) => Promise<void>;
  /** Forwarded to computeReconnectDelayMs for deterministic jitter in tests. */
  random?: () => number;
  /** Clock used for the maxWaitMs deadline; injectable for tests. Default: Date.now. */
  now?: () => number;
}

/**
 * Repeats the TabClientTransport handshake with backoff instead of the
 * transport's own one-shot mcp-check-ready. A page's WebMCP server
 * (@mcp-b/global, a lazy-loaded chunk, a native adapter, etc.) may start
 * after this content script does; TabClientTransport only probes once at
 * start(), so a late server is otherwise missed for the lifetime of the tab.
 */
export async function connectWithRetry<T extends ReadyAwareTransport>(
  deps: ConnectWithRetryDeps<T>,
): Promise<T> {
  const { createTransport, maxWaitMs = DEFAULT_MAX_WAIT_MS, wait = defaultWait, random, now = Date.now } = deps;
  const deadline = now() + maxWaitMs;
  let attempt = 0;

  for (;;) {
    if (now() >= deadline) {
      throw new Error("Timed out waiting for the page's WebMCP server to become ready");
    }

    const transport = createTransport();
    await transport.start();

    const ready = await Promise.race([
      transport.serverReadyPromise.then(() => true as const),
      wait(computeReconnectDelayMs(attempt, random)).then(() => false as const),
    ]);
    if (ready) return transport;

    await transport.close();
    attempt += 1;
  }
}
