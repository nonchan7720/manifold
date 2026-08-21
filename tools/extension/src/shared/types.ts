/** Edge WebSocket protocol frames, per docs/design/webmcp-reverse-gateway.ja.md ("Edge WebSocket プロトコル"). */

export interface AuthFrame {
  v: 1;
  type: "auth";
  token: string;
}

export interface ReadyFrame {
  type: "ready";
  heartbeatSec: number;
  origins: string[];
}

export interface AppUpFrame {
  type: "app.up";
  origin: string;
  appSession: string;
}

export interface AppDownFrame {
  type: "app.down";
  origin: string;
  appSession: string;
}

/** payload is raw MCP JSON-RPC — the extension relays it without inspecting it. */
export interface McpFrame {
  type: "mcp";
  origin: string;
  appSession: string;
  payload: unknown;
}

export interface PingFrame {
  type: "ping";
}

export interface PongFrame {
  type: "pong";
}

export interface ErrorFrame {
  type: "error";
  message: string;
}

/** Frames the extension sends to the edge server. */
export type OutgoingFrame = AuthFrame | AppUpFrame | AppDownFrame | McpFrame | PingFrame;

/** Frames the extension receives from the edge server. */
export type IncomingFrame = ReadyFrame | McpFrame | PongFrame | ErrorFrame;

/** A single bridged tab: a WebMCP-registered page connected through the content script. */
export interface TabBridge {
  origin: string;
  appSession: string;
  tabId: number;
  /** Relays an mcp frame's payload to this tab's content script. */
  send: (payload: unknown) => void;
}

/**
 * Structural subset of @mcp-b/transports' `Transport` interface that this
 * extension relies on. Kept separate from the library so background/content
 * code can be unit-tested against plain fakes instead of real postMessage or
 * chrome.runtime.Port channels.
 */
export interface TransportLike {
  start: () => Promise<void>;
  send: (message: unknown) => Promise<void>;
  close: () => Promise<void>;
  onmessage: ((message: unknown) => void) | null;
  onclose: (() => void) | null;
  onerror: ((error: unknown) => void) | null;
}
