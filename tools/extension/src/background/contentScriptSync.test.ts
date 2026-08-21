import { describe, expect, it, vi } from "vitest";
import {
  CONTENT_SCRIPT_ID,
  CONTENT_SCRIPT_PATH,
  NATIVE_ADAPTER_SCRIPT_ID,
  NATIVE_ADAPTER_SCRIPT_PATH,
  syncBridgeContentScript,
  syncNativeAdapterContentScript,
} from "./contentScriptSync";
import type { ScriptingApi } from "./contentScriptSync";

function createScriptingMock(registeredIds: string[] = []): ScriptingApi {
  return {
    getRegisteredContentScripts: vi.fn(async () => registeredIds.map((id) => ({ id }))),
    registerContentScripts: vi.fn(async () => undefined),
    updateContentScripts: vi.fn(async () => undefined),
    unregisterContentScripts: vi.fn(async () => undefined),
  };
}

describe("syncBridgeContentScript", () => {
  it("registers the bridge script for the given origins when none is registered yet", async () => {
    const scripting = createScriptingMock([]);

    await syncBridgeContentScript(["https://app1.example.com", "https://app2.example.com"], scripting);

    expect(scripting.registerContentScripts).toHaveBeenCalledWith([
      {
        id: CONTENT_SCRIPT_ID,
        matches: ["https://app1.example.com/*", "https://app2.example.com/*"],
        js: [CONTENT_SCRIPT_PATH],
        runAt: "document_idle",
      },
    ]);
    expect(scripting.updateContentScripts).not.toHaveBeenCalled();
  });

  it("updates the existing registration when the script is already registered", async () => {
    const scripting = createScriptingMock([CONTENT_SCRIPT_ID]);

    await syncBridgeContentScript(["https://app1.example.com"], scripting);

    expect(scripting.updateContentScripts).toHaveBeenCalledWith([
      {
        id: CONTENT_SCRIPT_ID,
        matches: ["https://app1.example.com/*"],
        js: [CONTENT_SCRIPT_PATH],
        runAt: "document_idle",
      },
    ]);
    expect(scripting.registerContentScripts).not.toHaveBeenCalled();
  });

  it("unregisters the bridge script when there are no allowed origins", async () => {
    const scripting = createScriptingMock([CONTENT_SCRIPT_ID]);

    await syncBridgeContentScript([], scripting);

    expect(scripting.unregisterContentScripts).toHaveBeenCalledWith({ ids: [CONTENT_SCRIPT_ID] });
    expect(scripting.registerContentScripts).not.toHaveBeenCalled();
    expect(scripting.updateContentScripts).not.toHaveBeenCalled();
  });

  it("does nothing when there are no allowed origins and nothing is registered", async () => {
    const scripting = createScriptingMock([]);

    await syncBridgeContentScript([], scripting);

    expect(scripting.unregisterContentScripts).not.toHaveBeenCalled();
  });
});

describe("syncNativeAdapterContentScript", () => {
  it("registers the native adapter script in the MAIN world for the given origins", async () => {
    const scripting = createScriptingMock([]);

    await syncNativeAdapterContentScript(["https://app1.example.com"], scripting);

    expect(scripting.registerContentScripts).toHaveBeenCalledWith([
      {
        id: NATIVE_ADAPTER_SCRIPT_ID,
        matches: ["https://app1.example.com/*"],
        js: [NATIVE_ADAPTER_SCRIPT_PATH],
        runAt: "document_idle",
        world: "MAIN",
      },
    ]);
  });

  it("updates the existing registration when already registered", async () => {
    const scripting = createScriptingMock([NATIVE_ADAPTER_SCRIPT_ID]);

    await syncNativeAdapterContentScript(["https://app1.example.com"], scripting);

    expect(scripting.updateContentScripts).toHaveBeenCalledWith([
      {
        id: NATIVE_ADAPTER_SCRIPT_ID,
        matches: ["https://app1.example.com/*"],
        js: [NATIVE_ADAPTER_SCRIPT_PATH],
        runAt: "document_idle",
        world: "MAIN",
      },
    ]);
    expect(scripting.registerContentScripts).not.toHaveBeenCalled();
  });

  it("unregisters the native adapter script when there are no allowed origins", async () => {
    const scripting = createScriptingMock([NATIVE_ADAPTER_SCRIPT_ID]);

    await syncNativeAdapterContentScript([], scripting);

    expect(scripting.unregisterContentScripts).toHaveBeenCalledWith({ ids: [NATIVE_ADAPTER_SCRIPT_ID] });
  });
});
