import { describe, expect, it, vi } from "vitest";
import type { ReadyAwareTransport } from "../shared/types";
import { connectWithRetry } from "./connectWithRetry";

function neverResolves<T>(): Promise<T> {
  return new Promise<T>(() => undefined);
}

function createFakeTransport(serverReadyPromise: Promise<void>): ReadyAwareTransport {
  return {
    start: vi.fn(async () => undefined),
    send: vi.fn(async () => undefined),
    close: vi.fn(async () => undefined),
    onmessage: null,
    onclose: null,
    onerror: null,
    serverReadyPromise,
  };
}

describe("connectWithRetry", () => {
  it("returns the first transport once its server-ready handshake resolves", async () => {
    const transport = createFakeTransport(Promise.resolve());
    const createTransport = vi.fn(() => transport);

    const result = await connectWithRetry({
      createTransport,
      wait: () => neverResolves(),
    });

    expect(result).toBe(transport);
    expect(createTransport).toHaveBeenCalledTimes(1);
    expect(transport.start).toHaveBeenCalledTimes(1);
    expect(transport.close).not.toHaveBeenCalled();
  });

  it("closes a transport that times out and retries with a fresh one", async () => {
    const staleTransport = createFakeTransport(neverResolves());
    const readyTransport = createFakeTransport(Promise.resolve());
    const createTransport = vi.fn().mockReturnValueOnce(staleTransport).mockReturnValueOnce(readyTransport);
    const wait = vi.fn(async () => undefined);

    const result = await connectWithRetry({ createTransport, wait });

    expect(result).toBe(readyTransport);
    expect(createTransport).toHaveBeenCalledTimes(2);
    expect(staleTransport.close).toHaveBeenCalledTimes(1);
    expect(readyTransport.close).not.toHaveBeenCalled();
  });

  it("passes growing backoff delays to wait across attempts", async () => {
    const staleTransport = createFakeTransport(neverResolves());
    const readyTransport = createFakeTransport(Promise.resolve());
    const createTransport = vi.fn().mockReturnValueOnce(staleTransport).mockReturnValueOnce(readyTransport);
    const wait = vi.fn(async () => undefined);

    await connectWithRetry({ createTransport, wait, random: () => 0 });

    expect(wait).toHaveBeenNthCalledWith(1, 500);
    expect(wait).toHaveBeenNthCalledWith(2, 1000);
  });

  it("gives up and rejects once maxWaitMs has elapsed", async () => {
    const staleTransport = createFakeTransport(neverResolves());
    const createTransport = vi.fn(() => staleTransport);
    const wait = vi.fn(async () => undefined);
    const now = vi.fn().mockReturnValueOnce(0).mockReturnValueOnce(0).mockReturnValueOnce(1500);

    await expect(connectWithRetry({ createTransport, wait, maxWaitMs: 1000, now })).rejects.toThrow(
      /timed out/i,
    );

    expect(createTransport).toHaveBeenCalledTimes(1);
    expect(staleTransport.close).toHaveBeenCalledTimes(1);
  });
});
