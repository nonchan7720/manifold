import { beforeEach, describe, expect, it } from "vitest";
import { clearEdgeToken, getEdgeSettings, saveEdgeToken, saveEdgeUrl } from "./storage";

/** Minimal fake of chrome.storage.local backed by an in-memory object. */
function createFakeStorageArea(): chrome.storage.StorageArea {
  let store: Record<string, unknown> = {};
  return {
    get: (async (keys?: unknown) => {
      if (keys == null) return { ...store };
      const names = Array.isArray(keys) ? keys : [keys as string];
      const result: Record<string, unknown> = {};
      for (const key of names) {
        if (key in store) result[key] = store[key];
      }
      return result;
    }) as chrome.storage.StorageArea["get"],
    set: (async (items: Record<string, unknown>) => {
      store = { ...store, ...items };
    }) as chrome.storage.StorageArea["set"],
    remove: (async (keys: string | string[]) => {
      const names = Array.isArray(keys) ? keys : [keys];
      for (const key of names) delete store[key];
    }) as chrome.storage.StorageArea["remove"],
  } as chrome.storage.StorageArea;
}

describe("storage", () => {
  let area: chrome.storage.StorageArea;

  beforeEach(() => {
    area = createFakeStorageArea();
  });

  it("returns undefined values before anything is saved", async () => {
    await expect(getEdgeSettings(area)).resolves.toEqual({ edgeUrl: undefined, edgeToken: undefined });
  });

  it("round-trips the edge URL", async () => {
    await saveEdgeUrl("ws://localhost:8081/edge/ws", area);
    await expect(getEdgeSettings(area)).resolves.toEqual({
      edgeUrl: "ws://localhost:8081/edge/ws",
      edgeToken: undefined,
    });
  });

  it("round-trips the edge token", async () => {
    await saveEdgeToken("edge-token-1", area);
    await expect(getEdgeSettings(area)).resolves.toEqual({
      edgeUrl: undefined,
      edgeToken: "edge-token-1",
    });
  });

  it("clears the edge token on logout without touching the edge URL", async () => {
    await saveEdgeUrl("ws://localhost:8081/edge/ws", area);
    await saveEdgeToken("edge-token-1", area);

    await clearEdgeToken(area);

    await expect(getEdgeSettings(area)).resolves.toEqual({
      edgeUrl: "ws://localhost:8081/edge/ws",
      edgeToken: undefined,
    });
  });
});
