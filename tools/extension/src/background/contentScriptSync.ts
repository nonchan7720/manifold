export const CONTENT_SCRIPT_ID = "webmcp-bridge";
export const CONTENT_SCRIPT_PATH = "src/content/index.js";

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
  const existing = await scripting.getRegisteredContentScripts({ ids: [CONTENT_SCRIPT_ID] });

  if (origins.length === 0) {
    if (existing.length > 0) {
      await scripting.unregisterContentScripts({ ids: [CONTENT_SCRIPT_ID] });
    }
    return;
  }

  const script: chrome.scripting.RegisteredContentScript = {
    id: CONTENT_SCRIPT_ID,
    matches: origins.map((origin) => `${origin}/*`),
    js: [CONTENT_SCRIPT_PATH],
    runAt: "document_idle",
  };

  if (existing.length > 0) {
    await scripting.updateContentScripts([script]);
  } else {
    await scripting.registerContentScripts([script]);
  }
}
