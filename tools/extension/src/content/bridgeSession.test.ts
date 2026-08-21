import { describe, expect, it, vi } from "vitest";
import type { TransportLike } from "../shared/types";
import { createBridgeSession } from "./bridgeSession";

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

describe("createBridgeSession", () => {
  it("bridges the page transport to a fresh extension transport on the first attempt", async () => {
    const pageTransport = createFakeTransport();
    const extensionTransport = createFakeTransport();
    const connectPageTransport = vi.fn(async () => pageTransport);
    const createExtensionTransport = vi.fn(() => extensionTransport);
    const session = createBridgeSession({ connectPageTransport, createExtensionTransport });

    await session.attempt();

    expect(connectPageTransport).toHaveBeenCalledTimes(1);
    expect(extensionTransport.start).toHaveBeenCalled();
    expect(session.isBridged()).toBe(true);
  });

  it("does nothing on a later attempt once already bridged", async () => {
    const connectPageTransport = vi.fn(async () => createFakeTransport());
    const createExtensionTransport = vi.fn(() => createFakeTransport());
    const session = createBridgeSession({ connectPageTransport, createExtensionTransport });
    await session.attempt();

    await session.attempt();

    expect(connectPageTransport).toHaveBeenCalledTimes(1);
  });

  it("coalesces concurrent attempts into a single in-flight run", async () => {
    let resolveConnect!: (transport: TransportLike) => void;
    const connectPageTransport = vi.fn(
      () =>
        new Promise<TransportLike>((resolve) => {
          resolveConnect = resolve;
        }),
    );
    const createExtensionTransport = vi.fn(() => createFakeTransport());
    const session = createBridgeSession({ connectPageTransport, createExtensionTransport });

    const first = session.attempt();
    const second = session.attempt();
    resolveConnect(createFakeTransport());
    await Promise.all([first, second]);

    expect(connectPageTransport).toHaveBeenCalledTimes(1);
  });

  it("logs and stays unbridged when the page transport never becomes ready", async () => {
    const error = new Error("Timed out waiting for the page's WebMCP server to become ready");
    const connectPageTransport = vi.fn(async () => {
      throw error;
    });
    const onError = vi.fn();
    const session = createBridgeSession({
      connectPageTransport,
      createExtensionTransport: vi.fn(() => createFakeTransport()),
      onError,
    });

    await session.attempt();

    expect(session.isBridged()).toBe(false);
    expect(onError).toHaveBeenCalledWith(expect.any(String), error);
  });

  it("retries on a later attempt after a failed one", async () => {
    const error = new Error("timeout");
    const connectPageTransport = vi
      .fn()
      .mockRejectedValueOnce(error)
      .mockResolvedValueOnce(createFakeTransport());
    const session = createBridgeSession({
      connectPageTransport,
      createExtensionTransport: vi.fn(() => createFakeTransport()),
      onError: vi.fn(),
    });

    await session.attempt();
    expect(session.isBridged()).toBe(false);

    await session.attempt();
    expect(session.isBridged()).toBe(true);
    expect(connectPageTransport).toHaveBeenCalledTimes(2);
  });

  it("re-bridges with a fresh transport after the extension connection is lost", async () => {
    const firstExtensionTransport = createFakeTransport();
    const secondExtensionTransport = createFakeTransport();
    const connectPageTransport = vi
      .fn()
      .mockResolvedValueOnce(createFakeTransport())
      .mockResolvedValueOnce(createFakeTransport());
    const createExtensionTransport = vi
      .fn()
      .mockReturnValueOnce(firstExtensionTransport)
      .mockReturnValueOnce(secondExtensionTransport);
    const session = createBridgeSession({ connectPageTransport, createExtensionTransport });
    await session.attempt();
    expect(session.isBridged()).toBe(true);

    firstExtensionTransport.onclose?.();
    expect(session.isBridged()).toBe(false);

    await session.attempt();

    expect(session.isBridged()).toBe(true);
    expect(connectPageTransport).toHaveBeenCalledTimes(2);
  });
});
