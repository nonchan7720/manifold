import type { PopupState } from "./popupController";

export interface PopupView {
  edgeUrlValue: string;
  showForm: boolean;
  showLogout: boolean;
  statusText: string;
  errorText: string;
}

/** Pure state -> view-model mapping; main.ts only applies this to the DOM. */
export function renderState(state: PopupState): PopupView {
  return {
    edgeUrlValue: state.edgeUrl,
    showForm: !state.paired,
    showLogout: state.paired,
    statusText: state.paired ? statusText(state) : "",
    errorText: state.error ?? "",
  };
}

function statusText(state: PopupState): string {
  if (!state.status) return "Status: unknown";
  const origins = state.status.connectedOrigins;
  return `Status: ${state.status.status} — connected: ${origins.length > 0 ? origins.join(", ") : "none"}`;
}
