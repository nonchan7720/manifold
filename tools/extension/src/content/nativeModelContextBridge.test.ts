import { describe, expect, it, vi } from "vitest";
import type { TransportLike } from "../shared/types";
import { createNativeModelContextBridge } from "./nativeModelContextBridge";
import type { BrowserServerLike, NativeModelContextLike } from "./nativeModelContextBridge";

function createFakeTransport(): TransportLike {
  return {
    start: vi.fn(async () => undefined),
    send: vi.fn(async () => undefined),
    close: vi.fn(async () => undefined),
    onmessage: null,
    onclose: null,
    onerror: null,
  };
}

function createFakeNative(): NativeModelContextLike & { dispatch: (type: string) => void } {
  const listeners = new Map<string, Set<() => void>>();
  return {
    getTools: vi.fn(async () => []),
    executeTool: vi.fn(async () => null),
    addEventListener: vi.fn((type: string, listener: () => void) => {
      const set = listeners.get(type) ?? new Set();
      set.add(listener);
      listeners.set(type, set);
    }),
    dispatch(type: string) {
      for (const listener of listeners.get(type) ?? []) listener();
    },
  };
}

function createFakeServer(): BrowserServerLike {
  return {
    syncNativeTools: vi.fn(() => 0),
    connect: vi.fn(async () => undefined),
  };
}

describe("createNativeModelContextBridge", () => {
  it("does nothing when there is no native model context", async () => {
    const createServer = vi.fn();
    const bridge = createNativeModelContextBridge({
      native: undefined,
      isAlreadyBridged: () => false,
      createServer,
      createTransport: createFakeTransport,
    });

    await bridge.start();

    expect(createServer).not.toHaveBeenCalled();
  });

  it("does nothing when the native context is already a BrowserMcpServer (e.g. @mcp-b/global present)", async () => {
    const createServer = vi.fn();
    const bridge = createNativeModelContextBridge({
      native: createFakeNative(),
      isAlreadyBridged: () => true,
      createServer,
      createTransport: createFakeTransport,
    });

    await bridge.start();

    expect(createServer).not.toHaveBeenCalled();
  });

  it("does nothing when the native object lacks getTools/executeTool", async () => {
    const createServer = vi.fn();
    const bridge = createNativeModelContextBridge({
      native: { addEventListener: vi.fn() },
      isAlreadyBridged: () => false,
      createServer,
      createTransport: createFakeTransport,
    });

    await bridge.start();

    expect(createServer).not.toHaveBeenCalled();
  });

  it("wraps the native context: backfills tools, connects the transport, and re-syncs on toolchange", async () => {
    const native = createFakeNative();
    const server = createFakeServer();
    const transport = createFakeTransport();
    const createServer = vi.fn(() => server);
    const createTransport = vi.fn(() => transport);

    const bridge = createNativeModelContextBridge({
      native,
      isAlreadyBridged: () => false,
      createServer,
      createTransport,
    });

    await bridge.start();

    expect(createServer).toHaveBeenCalledWith(native);
    expect(server.syncNativeTools).toHaveBeenCalledTimes(1);
    expect(server.connect).toHaveBeenCalledWith(transport);

    native.dispatch("toolchange");

    expect(server.syncNativeTools).toHaveBeenCalledTimes(2);
  });
});
