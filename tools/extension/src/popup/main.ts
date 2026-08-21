import { createPopupController } from "./popupController";
import { renderState } from "./view";
import "./popup.css";

const controller = createPopupController({
  storageArea: chrome.storage.local,
  sendRuntimeMessage: (message) => chrome.runtime.sendMessage(message),
});

const form = document.querySelector<HTMLFormElement>("#pair-form");
const edgeUrlInput = document.querySelector<HTMLInputElement>("#edge-url");
const codeInput = document.querySelector<HTMLInputElement>("#pairing-code");
const logoutButton = document.querySelector<HTMLButtonElement>("#logout-button");
const statusParagraph = document.querySelector<HTMLElement>("#status");
const errorParagraph = document.querySelector<HTMLElement>("#error");

function render(state: Awaited<ReturnType<typeof controller.loadState>>) {
  const view = renderState(state);
  if (edgeUrlInput) edgeUrlInput.value = view.edgeUrlValue;
  if (form) form.hidden = !view.showForm;
  if (logoutButton) logoutButton.hidden = !view.showLogout;
  if (statusParagraph) statusParagraph.textContent = view.statusText;
  if (errorParagraph) errorParagraph.textContent = view.errorText;
}

void controller.loadState().then(render);

form?.addEventListener("submit", (event) => {
  event.preventDefault();
  const edgeUrl = edgeUrlInput?.value.trim() ?? "";
  const code = codeInput?.value.trim() ?? "";
  void controller.pair(edgeUrl, code).then(render);
});

logoutButton?.addEventListener("click", () => {
  void controller.logout().then(render);
});
