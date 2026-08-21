import { describe, expect, it, vi } from "vitest";
import type { TransportLike } from "../shared/types";
import { createPageBridge } from "./pageBridge";

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

describe("createPageBridge", () => {
  it("starts the extension transport, but not the page transport (already started by the caller's handshake retry)", async () => {
    const pageTransport = createFakeTransport();
    const extensionTransport = createFakeTransport();
    const bridge = createPageBridge({ pageTransport, extensionTransport });

    await bridge.start();

    expect(pageTransport.start).not.toHaveBeenCalled();
    expect(extensionTransport.start).toHaveBeenCalled();
  });

  it("relays a message from the page transport to the extension transport", async () => {
    const pageTransport = createFakeTransport();
    const extensionTransport = createFakeTransport();
    const bridge = createPageBridge({ pageTransport, extensionTransport });
    await bridge.start();

    const message = { jsonrpc: "2.0", id: 1, result: {} };
    pageTransport.onmessage?.(message);

    expect(extensionTransport.send).toHaveBeenCalledWith(message);
  });

  it("relays a message from the extension transport to the page transport", async () => {
    const pageTransport = createFakeTransport();
    const extensionTransport = createFakeTransport();
    const bridge = createPageBridge({ pageTransport, extensionTransport });
    await bridge.start();

    const message = { jsonrpc: "2.0", id: 1, method: "tools/call" };
    extensionTransport.onmessage?.(message);

    expect(pageTransport.send).toHaveBeenCalledWith(message);
  });

  it("closes both transports", async () => {
    const pageTransport = createFakeTransport();
    const extensionTransport = createFakeTransport();
    const bridge = createPageBridge({ pageTransport, extensionTransport });
    await bridge.start();

    await bridge.close();

    expect(pageTransport.close).toHaveBeenCalled();
    expect(extensionTransport.close).toHaveBeenCalled();
  });

  it("closes the page transport when the extension transport closes (background lost)", async () => {
    const pageTransport = createFakeTransport();
    const extensionTransport = createFakeTransport();
    const bridge = createPageBridge({ pageTransport, extensionTransport });
    await bridge.start();

    extensionTransport.onclose?.();

    expect(pageTransport.close).toHaveBeenCalled();
  });
});
