import type {
  AppDownFrame,
  AppUpFrame,
  AuthFrame,
  IncomingFrame,
  McpFrame,
  PingFrame,
} from "./types";

export function buildAuthFrame(token: string): AuthFrame {
  return { v: 1, type: "auth", token };
}

export function buildAppUpFrame(origin: string, appSession: string): AppUpFrame {
  return { type: "app.up", origin, appSession };
}

export function buildAppDownFrame(origin: string, appSession: string): AppDownFrame {
  return { type: "app.down", origin, appSession };
}

export function buildMcpFrame(origin: string, appSession: string, payload: unknown): McpFrame {
  return { type: "mcp", origin, appSession, payload };
}

export function buildPingFrame(): PingFrame {
  return { type: "ping" };
}

/** Parses a raw WS text frame into a known incoming frame, or null if malformed/unrecognized. */
export function parseIncomingFrame(raw: string): IncomingFrame | null {
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof data !== "object" || data === null || !("type" in data)) {
    return null;
  }
  const frame = data as Record<string, unknown>;
  switch (frame.type) {
    case "ready":
      if (typeof frame.heartbeatSec !== "number" || !Array.isArray(frame.origins)) {
        return null;
      }
      return {
        type: "ready",
        heartbeatSec: frame.heartbeatSec,
        origins: frame.origins as string[],
      };
    case "mcp":
      if (typeof frame.origin !== "string" || typeof frame.appSession !== "string") {
        return null;
      }
      return {
        type: "mcp",
        origin: frame.origin,
        appSession: frame.appSession,
        payload: frame.payload,
      };
    case "pong":
      return { type: "pong" };
    case "error":
      if (typeof frame.message !== "string") {
        return null;
      }
      return { type: "error", message: frame.message };
    default:
      return null;
  }
}
