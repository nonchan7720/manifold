import { describe, expect, it } from "vitest";
import {
  RECONNECT_BRIDGE_MESSAGE,
  RECONNECT_REQUEST_MESSAGE,
  isReconnectBridgeMessage,
  isReconnectRequestMessage,
} from "./messages";

describe("isReconnectBridgeMessage", () => {
  it("recognizes the reconnect-webmcp-bridge message", () => {
    expect(isReconnectBridgeMessage(RECONNECT_BRIDGE_MESSAGE)).toBe(true);
    expect(isReconnectBridgeMessage({ type: "reconnect-webmcp-bridge" })).toBe(true);
  });

  it("rejects unrelated messages", () => {
    expect(isReconnectBridgeMessage({ type: "get-status" })).toBe(false);
    expect(isReconnectBridgeMessage(RECONNECT_REQUEST_MESSAGE)).toBe(false);
    expect(isReconnectBridgeMessage(undefined)).toBe(false);
    expect(isReconnectBridgeMessage(null)).toBe(false);
    expect(isReconnectBridgeMessage("reconnect-webmcp-bridge")).toBe(false);
  });
});

describe("isReconnectRequestMessage", () => {
  it("recognizes the reconnect-request message", () => {
    expect(isReconnectRequestMessage(RECONNECT_REQUEST_MESSAGE)).toBe(true);
    expect(isReconnectRequestMessage({ type: "reconnect-request" })).toBe(true);
  });

  it("rejects unrelated messages", () => {
    expect(isReconnectRequestMessage({ type: "get-status" })).toBe(false);
    expect(isReconnectRequestMessage(RECONNECT_BRIDGE_MESSAGE)).toBe(false);
    expect(isReconnectRequestMessage(undefined)).toBe(false);
    expect(isReconnectRequestMessage(null)).toBe(false);
  });
});
