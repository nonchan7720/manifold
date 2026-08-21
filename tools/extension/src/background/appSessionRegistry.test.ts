import { describe, expect, it, vi } from "vitest";
import { createAppSessionRegistry } from "./appSessionRegistry";

describe("createAppSessionRegistry", () => {
  it("has no entries initially", () => {
    const registry = createAppSessionRegistry();
    expect(registry.list()).toEqual([]);
    expect(registry.get("session-1")).toBeUndefined();
  });

  it("registers and retrieves a tab bridge by appSession", () => {
    const registry = createAppSessionRegistry();
    const send = vi.fn();
    registry.register({ origin: "https://app1.example.com", appSession: "session-1", tabId: 1, send });

    expect(registry.get("session-1")).toEqual({
      origin: "https://app1.example.com",
      appSession: "session-1",
      tabId: 1,
      send,
    });
    expect(registry.list()).toHaveLength(1);
  });

  it("replaces an existing entry when the same appSession registers again", () => {
    const registry = createAppSessionRegistry();
    registry.register({ origin: "https://app1.example.com", appSession: "session-1", tabId: 1, send: vi.fn() });
    const secondSend = vi.fn();
    registry.register({ origin: "https://app1.example.com", appSession: "session-1", tabId: 1, send: secondSend });

    expect(registry.list()).toHaveLength(1);
    expect(registry.get("session-1")?.send).toBe(secondSend);
  });

  it("removes an entry on unregister", () => {
    const registry = createAppSessionRegistry();
    registry.register({ origin: "https://app1.example.com", appSession: "session-1", tabId: 1, send: vi.fn() });

    registry.unregister("session-1");

    expect(registry.get("session-1")).toBeUndefined();
    expect(registry.list()).toEqual([]);
  });

  it("unregistering an unknown appSession is a no-op", () => {
    const registry = createAppSessionRegistry();
    expect(() => registry.unregister("does-not-exist")).not.toThrow();
  });

  it("lists multiple entries across different tabs", () => {
    const registry = createAppSessionRegistry();
    registry.register({ origin: "https://app1.example.com", appSession: "session-1", tabId: 1, send: vi.fn() });
    registry.register({ origin: "https://app2.example.com", appSession: "session-2", tabId: 2, send: vi.fn() });

    expect(registry.list().map((entry) => entry.appSession).sort()).toEqual(["session-1", "session-2"]);
  });
});
