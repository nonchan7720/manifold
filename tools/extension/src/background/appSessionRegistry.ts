import type { TabBridge } from "../shared/types";

export interface AppSessionRegistry {
  register: (entry: TabBridge) => void;
  /**
   * Removes entry only if it is still the current binding for its
   * appSession, returning whether it was removed. A reconnect can register a
   * new TabBridge under the same appSession before the old one's close
   * callback runs; without this check that later unregister(oldEntry) would
   * delete the new, still-live binding.
   */
  unregister: (entry: TabBridge) => boolean;
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
    unregister(entry) {
      if (entries.get(entry.appSession) !== entry) return false;
      entries.delete(entry.appSession);
      return true;
    },
    get(appSession) {
      return entries.get(appSession);
    },
    list() {
      return [...entries.values()];
    },
  };
}
