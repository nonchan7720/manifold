import { describe, expect, it } from "vitest";
import { renderState } from "./view";

describe("renderState", () => {
  it("shows the pairing form and no status when unpaired", () => {
    expect(renderState({ edgeUrl: "", paired: false, status: undefined, error: undefined })).toEqual({
      edgeUrlValue: "",
      showForm: true,
      showLogout: false,
      statusText: "",
      errorText: "",
    });
  });

  it("shows a pairing error", () => {
    const view = renderState({
      edgeUrl: "ws://localhost:8081/edge/ws",
      paired: false,
      status: undefined,
      error: "pairing failed with status 401",
    });
    expect(view.errorText).toBe("pairing failed with status 401");
    expect(view.showForm).toBe(true);
  });

  it("hides the form and shows connected origins once paired", () => {
    const view = renderState({
      edgeUrl: "ws://localhost:8081/edge/ws",
      paired: true,
      status: { status: "ready", allowedOrigins: ["https://app1.example.com"], connectedOrigins: ["https://app1.example.com"] },
      error: undefined,
    });
    expect(view).toEqual({
      edgeUrlValue: "ws://localhost:8081/edge/ws",
      showForm: false,
      showLogout: true,
      statusText: "Status: ready — connected: https://app1.example.com",
      errorText: "",
    });
  });

  it("reports no connected tabs when paired but nothing is bridged", () => {
    const view = renderState({
      edgeUrl: "ws://localhost:8081/edge/ws",
      paired: true,
      status: { status: "ready", allowedOrigins: ["https://app1.example.com"], connectedOrigins: [] },
      error: undefined,
    });
    expect(view.statusText).toBe("Status: ready — connected: none");
  });

  it("reports an unknown status when paired but the background hasn't responded", () => {
    const view = renderState({
      edgeUrl: "ws://localhost:8081/edge/ws",
      paired: true,
      status: undefined,
      error: undefined,
    });
    expect(view.statusText).toBe("Status: unknown");
  });
});
