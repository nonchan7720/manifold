import { GET_STATUS_MESSAGE } from "../background/app";
import type { EdgeStatusSnapshot } from "../background/app";
import { RECONNECT_REQUEST_MESSAGE } from "../shared/messages";
import { clearEdgeToken, getEdgeSettings, saveEdgeToken, saveEdgeUrl } from "../shared/storage";
import { exchangePairingCode } from "./pairing";

export interface PopupState {
  edgeUrl: string;
  paired: boolean;
  status: EdgeStatusSnapshot | undefined;
  error: string | undefined;
}

export interface PopupControllerDeps {
  storageArea: chrome.storage.StorageArea;
  fetchImpl?: typeof fetch;
  /** Queries the background service worker's current edge connection status. */
  sendRuntimeMessage: (message: unknown) => Promise<unknown>;
}

export interface PopupController {
  loadState: () => Promise<PopupState>;
  pair: (edgeUrl: string, code: string) => Promise<PopupState>;
  logout: () => Promise<PopupState>;
  reconnect: () => Promise<PopupState>;
}

export function createPopupController(deps: PopupControllerDeps): PopupController {
  async function queryStatus(): Promise<EdgeStatusSnapshot | undefined> {
    try {
      return (await deps.sendRuntimeMessage(GET_STATUS_MESSAGE)) as EdgeStatusSnapshot;
    } catch {
      // The background service worker may not have started yet (e.g. right
      // after install), or there's nothing paired to report on.
      return undefined;
    }
  }

  async function loadState(): Promise<PopupState> {
    const settings = await getEdgeSettings(deps.storageArea);
    const paired = Boolean(settings.edgeToken);
    return {
      edgeUrl: settings.edgeUrl ?? "",
      paired,
      status: paired ? await queryStatus() : undefined,
      error: undefined,
    };
  }

  return {
    loadState,
    async pair(edgeUrl, code) {
      await saveEdgeUrl(edgeUrl, deps.storageArea);
      try {
        const token = await exchangePairingCode(edgeUrl, code, deps.fetchImpl);
        await saveEdgeToken(token, deps.storageArea);
        return await loadState();
      } catch (error) {
        return {
          edgeUrl,
          paired: false,
          status: undefined,
          error: error instanceof Error ? error.message : String(error),
        };
      }
    },
    async logout() {
      await clearEdgeToken(deps.storageArea);
      return loadState();
    },
    async reconnect() {
      try {
        await deps.sendRuntimeMessage(RECONNECT_REQUEST_MESSAGE);
      } catch {
        // Background may be unreachable; loadState() reflects what it can.
      }
      return loadState();
    },
  };
}
