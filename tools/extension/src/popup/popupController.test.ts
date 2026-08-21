import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPopupController } from "./popupController";
import { GET_STATUS_MESSAGE } from "../background/app";
import { RECONNECT_REQUEST_MESSAGE } from "../shared/messages";

function createFakeStorageArea(): chrome.storage.StorageArea {
  let store: Record<string, unknown> = {};
  return {
    get: (async (keys?: unknown) => {
      const names = Array.isArray(keys) ? keys : keys ? [keys as string] : Object.keys(store);
      const result: Record<string, unknown> = {};
      for (const key of names) if (key in store) result[key] = store[key];
      return result;
    }) as chrome.storage.StorageArea["get"],
    set: (async (items: Record<string, unknown>) => {
      store = { ...store, ...items };
    }) as chrome.storage.StorageArea["set"],
    remove: (async (keys: string | string[]) => {
      for (const key of Array.isArray(keys) ? keys : [keys]) delete store[key];
    }) as chrome.storage.StorageArea["remove"],
  } as chrome.storage.StorageArea;
}

describe("createPopupController", () => {
  let storageArea: chrome.storage.StorageArea;

  beforeEach(() => {
    storageArea = createFakeStorageArea();
  });

  it("reports an unpaired state when no token is stored", async () => {
    const controller = createPopupController({ storageArea, sendRuntimeMessage: vi.fn() });

    const state = await controller.loadState();

    expect(state).toEqual({ edgeUrl: "", paired: false, status: undefined, error: undefined });
  });

  it("pairs by saving the edge URL, exchanging the code, and storing the token", async () => {
    const fetchImpl = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ token: "edge-token-1" }),
    })) as unknown as typeof fetch;
    const sendRuntimeMessage = vi.fn(async () => ({
      status: "ready",
      allowedOrigins: ["https://app1.example.com"],
      connectedOrigins: [],
    }));
    const controller = createPopupController({ storageArea, fetchImpl, sendRuntimeMessage });

    const state = await controller.pair("ws://localhost:8081/edge/ws", "12345678");

    expect(state.paired).toBe(true);
    expect(state.edgeUrl).toBe("ws://localhost:8081/edge/ws");
    expect(state.error).toBeUndefined();
    expect(state.status).toEqual({
      status: "ready",
      allowedOrigins: ["https://app1.example.com"],
      connectedOrigins: [],
    });
    expect(sendRuntimeMessage).toHaveBeenCalledWith(GET_STATUS_MESSAGE);
  });

  it("surfaces a pairing error without storing a token", async () => {
    const fetchImpl = vi.fn(async () => ({
      ok: false,
      status: 401,
      json: async () => ({}),
    })) as unknown as typeof fetch;
    const controller = createPopupController({ storageArea, fetchImpl, sendRuntimeMessage: vi.fn() });

    const state = await controller.pair("ws://localhost:8081/edge/ws", "bad-code");

    expect(state.paired).toBe(false);
    expect(state.error).toBeTruthy();
  });

  it("logs out by clearing the token but keeping the edge URL", async () => {
    const fetchImpl = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ token: "edge-token-1" }),
    })) as unknown as typeof fetch;
    const controller = createPopupController({ storageArea, fetchImpl, sendRuntimeMessage: vi.fn(async () => undefined) });
    await controller.pair("ws://localhost:8081/edge/ws", "12345678");

    const state = await controller.logout();

    expect(state).toEqual({
      edgeUrl: "ws://localhost:8081/edge/ws",
      paired: false,
      status: undefined,
      error: undefined,
    });
  });

  it("reconnects by asking the background to reconnect and reloading state", async () => {
    const fetchImpl = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ token: "edge-token-1" }),
    })) as unknown as typeof fetch;
    const sendRuntimeMessage = vi.fn(async () => ({
      status: "ready",
      allowedOrigins: ["https://app1.example.com"],
      connectedOrigins: ["https://app1.example.com"],
    }));
    const controller = createPopupController({ storageArea, fetchImpl, sendRuntimeMessage });
    await controller.pair("ws://localhost:8081/edge/ws", "12345678");

    const state = await controller.reconnect();

    expect(sendRuntimeMessage).toHaveBeenCalledWith(RECONNECT_REQUEST_MESSAGE);
    expect(state.status?.connectedOrigins).toEqual(["https://app1.example.com"]);
  });

  it("still reloads state if the background is unreachable when reconnecting", async () => {
    const sendRuntimeMessage = vi.fn(async () => {
      throw new Error("no receiver");
    });
    const controller = createPopupController({ storageArea, sendRuntimeMessage });

    const state = await controller.reconnect();

    expect(state.status).toBeUndefined();
    expect(state.paired).toBe(false);
  });

  it("leaves status undefined when the background does not respond", async () => {
    const fetchImpl = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ token: "edge-token-1" }),
    })) as unknown as typeof fetch;
    const sendRuntimeMessage = vi.fn(async () => {
      throw new Error("no receiver");
    });
    const controller = createPopupController({ storageArea, fetchImpl, sendRuntimeMessage });

    const state = await controller.pair("ws://localhost:8081/edge/ws", "12345678");

    expect(state.paired).toBe(true);
    expect(state.status).toBeUndefined();
  });
});
