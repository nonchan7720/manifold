export interface EdgeSettings {
  edgeUrl: string | undefined;
  edgeToken: string | undefined;
}

export const EDGE_URL_KEY = "edgeUrl";
export const EDGE_TOKEN_KEY = "edgeToken";

function defaultArea(): chrome.storage.StorageArea {
  return chrome.storage.local;
}

export async function getEdgeSettings(
  area: chrome.storage.StorageArea = defaultArea(),
): Promise<EdgeSettings> {
  const stored = await area.get([EDGE_URL_KEY, EDGE_TOKEN_KEY]);
  return {
    edgeUrl: stored[EDGE_URL_KEY] as string | undefined,
    edgeToken: stored[EDGE_TOKEN_KEY] as string | undefined,
  };
}

export async function saveEdgeUrl(
  edgeUrl: string,
  area: chrome.storage.StorageArea = defaultArea(),
): Promise<void> {
  await area.set({ [EDGE_URL_KEY]: edgeUrl });
}

export async function saveEdgeToken(
  edgeToken: string,
  area: chrome.storage.StorageArea = defaultArea(),
): Promise<void> {
  await area.set({ [EDGE_TOKEN_KEY]: edgeToken });
}

export async function clearEdgeToken(
  area: chrome.storage.StorageArea = defaultArea(),
): Promise<void> {
  await area.remove(EDGE_TOKEN_KEY);
}
