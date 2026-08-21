/** background -> content: (re)attempt connectWithRetry's finite handshake window. */
export const RECONNECT_BRIDGE_MESSAGE = { type: "reconnect-webmcp-bridge" } as const;

export function isReconnectBridgeMessage(message: unknown): boolean {
  return (
    typeof message === "object" &&
    message !== null &&
    (message as { type?: unknown }).type === RECONNECT_BRIDGE_MESSAGE.type
  );
}

/** popup -> background: "Reconnect" button click. */
export const RECONNECT_REQUEST_MESSAGE = { type: "reconnect-request" } as const;

export function isReconnectRequestMessage(message: unknown): boolean {
  return (
    typeof message === "object" &&
    message !== null &&
    (message as { type?: unknown }).type === RECONNECT_REQUEST_MESSAGE.type
  );
}
