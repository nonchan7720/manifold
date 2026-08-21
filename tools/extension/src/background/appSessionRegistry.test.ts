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
    const entry = { origin: "https://app1.example.com", appSession: "session-1", tabId: 1, send: vi.fn() };
    registry.register(entry);

    expect(registry.unregister(entry)).toBe(true);
    expect(registry.get("session-1")).toBeUndefined();
    expect(registry.list()).toEqual([]);
  });

  it("unregistering an entry that was never registered is a no-op", () => {
    const registry = createAppSessionRegistry();
    const entry = { origin: "https://app1.example.com", appSession: "does-not-exist", tabId: 1, send: vi.fn() };
    expect(() => registry.unregister(entry)).not.toThrow();
    expect(registry.unregister(entry)).toBe(false);
  });

  it("does not remove a newer entry when an old, already-replaced entry unregisters", () => {
    // A reconnect can reuse the same appSession (port name) before the old
    // transport's close callback runs; unregister must only ever remove the
    // exact entry it was given, not whatever is currently stored under that
    // key.
    const registry = createAppSessionRegistry();
    const oldEntry = { origin: "https://app1.example.com", appSession: "session-1", tabId: 1, send: vi.fn() };
    const newEntry = { origin: "https://app1.example.com", appSession: "session-1", tabId: 1, send: vi.fn() };
    registry.register(oldEntry);
    registry.register(newEntry);

    expect(registry.unregister(oldEntry)).toBe(false);
    expect(registry.get("session-1")).toBe(newEntry);
  });

  it("lists multiple entries across different tabs", () => {
    const registry = createAppSessionRegistry();
    registry.register({ origin: "https://app1.example.com", appSession: "session-1", tabId: 1, send: vi.fn() });
    registry.register({ origin: "https://app2.example.com", appSession: "session-2", tabId: 2, send: vi.fn() });

    expect(registry.list().map((entry) => entry.appSession).sort()).toEqual(["session-1", "session-2"]);
  });
});
