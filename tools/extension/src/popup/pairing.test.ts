import { describe, expect, it, vi } from "vitest";
import { exchangePairingCode } from "./pairing";

function fetchReturning(body: unknown, ok = true, status = 200) {
  return vi.fn(async () => ({
    ok,
    status,
    json: async () => body,
  })) as unknown as typeof fetch;
}

describe("exchangePairingCode", () => {
  it("POSTs the code to {edge origin}/edge/pair and returns the edge token", async () => {
    const fetchImpl = fetchReturning({ token: "edge-token-1" });

    const token = await exchangePairingCode("ws://localhost:8081/edge/ws", "12345678", fetchImpl);

    expect(token).toBe("edge-token-1");
    expect(fetchImpl).toHaveBeenCalledWith(
      "http://localhost:8081/edge/pair",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({ "Content-Type": "application/json" }),
        body: JSON.stringify({ code: "12345678" }),
      }),
    );
  });

  it("maps wss:// edge URLs to https:// for the pairing endpoint", async () => {
    const fetchImpl = fetchReturning({ token: "edge-token-1" });

    await exchangePairingCode("wss://agent.example.com/edge/ws", "12345678", fetchImpl);

    expect(fetchImpl).toHaveBeenCalledWith("https://agent.example.com/edge/pair", expect.anything());
  });

  it("throws when the server responds with a non-OK status", async () => {
    const fetchImpl = fetchReturning({ error: "invalid code" }, false, 401);

    await expect(exchangePairingCode("ws://localhost:8081/edge/ws", "bad-code", fetchImpl)).rejects.toThrow();
  });

  it("throws when the response has no token", async () => {
    const fetchImpl = fetchReturning({});

    await expect(exchangePairingCode("ws://localhost:8081/edge/ws", "12345678", fetchImpl)).rejects.toThrow();
  });
});
