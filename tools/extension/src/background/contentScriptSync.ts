export const CONTENT_SCRIPT_ID = "webmcp-bridge";
export const CONTENT_SCRIPT_PATH = "src/content/index.js";
export const NATIVE_ADAPTER_SCRIPT_ID = "webmcp-native-adapter";
export const NATIVE_ADAPTER_SCRIPT_PATH = "src/content/nativeAdapter.js";

export interface ScriptingApi {
  getRegisteredContentScripts: (
    filter?: chrome.scripting.ContentScriptFilter,
  ) => Promise<{ id: string }[]>;
  registerContentScripts: (
    scripts: chrome.scripting.RegisteredContentScript[],
  ) => Promise<void>;
  updateContentScripts: (
    scripts: chrome.scripting.RegisteredContentScript[],
  ) => Promise<void>;
  unregisterContentScripts: (filter?: chrome.scripting.ContentScriptFilter) => Promise<void>;
}

async function syncContentScript(
  script: chrome.scripting.RegisteredContentScript,
  origins: string[],
  scripting: ScriptingApi,
): Promise<void> {
  const existing = await scripting.getRegisteredContentScripts({ ids: [script.id] });

  if (origins.length === 0) {
    if (existing.length > 0) {
      await scripting.unregisterContentScripts({ ids: [script.id] });
    }
    return;
  }

  if (existing.length > 0) {
    await scripting.updateContentScripts([script]);
  } else {
    await scripting.registerContentScripts([script]);
  }
}

/**
 * Registers (or updates) the bridge content script for exactly the origins the
 * edge server allows, per "ready で受け取った許可 origin のタブでのみ動作".
 * The extension has no static content_scripts entry; this runs whenever a
 * `ready` frame is (re)received.
 */
export async function syncBridgeContentScript(
  origins: string[],
  scripting: ScriptingApi,
): Promise<void> {
  await syncContentScript(
    {
      id: CONTENT_SCRIPT_ID,
      matches: origins.map((origin) => `${origin}/*`),
      js: [CONTENT_SCRIPT_PATH],
      runAt: "document_idle",
    },
    origins,
    scripting,
  );
}

/**
 * Registers (or updates) the native document.modelContext adapter in the
 * page's MAIN world, for the same origins as the bridge content script. It
 * no-ops on pages without a native, unwrapped document.modelContext (see
 * nativeModelContextBridge.ts), so registering it broadly for every allowed
 * origin is safe.
 */
export async function syncNativeAdapterContentScript(
  origins: string[],
  scripting: ScriptingApi,
): Promise<void> {
  await syncContentScript(
    {
      id: NATIVE_ADAPTER_SCRIPT_ID,
      matches: origins.map((origin) => `${origin}/*`),
      js: [NATIVE_ADAPTER_SCRIPT_PATH],
      runAt: "document_idle",
      world: "MAIN",
    },
    origins,
    scripting,
  );
}
