import type { TabBridge } from "../shared/types";

export interface AppSessionRegistry {
  register: (entry: TabBridge) => void;
  unregister: (appSession: string) => void;
  get: (appSession: string) => TabBridge | undefined;
  list: () => TabBridge[];
}

/** Tracks the tabs currently bridged to the edge connection, keyed by appSession. */
export function createAppSessionRegistry(): AppSessionRegistry {
  const entries = new Map<string, TabBridge>();

  return {
    register(entry) {
      entries.set(entry.appSession, entry);
    },
    unregister(appSession) {
      entries.delete(appSession);
    },
    get(appSession) {
      return entries.get(appSession);
    },
    list() {
      return [...entries.values()];
    },
  };
}
