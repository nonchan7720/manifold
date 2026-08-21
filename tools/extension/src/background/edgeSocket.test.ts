import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createAppSessionRegistry } from "./appSessionRegistry";
import { createEdgeConnection } from "./edgeSocket";
import type { WebSocketLike } from "./edgeSocket";

class FakeWebSocket implements WebSocketLike {
  static instances: FakeWebSocket[] = [];
  readyState = 0;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onclose: ((event: { code: number }) => void) | null = null;
  onerror: ((event: unknown) => void) | null = null;

  constructor(public url: string) {
    FakeWebSocket.instances.push(this);
  }

  send(data: string) {
    this.sent.push(data);
  }

  close(code = 1000) {
    this.readyState = 3;
    this.onclose?.({ code });
  }

  open() {
    this.readyState = 1;
    this.onopen?.();
  }

  receive(frame: unknown) {
    this.onmessage?.({ data: JSON.stringify(frame) });
  }
}

function lastSocket(): FakeWebSocket {
  const socket = FakeWebSocket.instances.at(-1);
  if (!socket) throw new Error("no socket created");
  return socket;
}

describe("createEdgeConnection", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeWebSocket.instances = [];
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  function setup() {
    const registry = createAppSessionRegistry();
    const onReady = vi.fn();
    const connection = createEdgeConnection({
      connectSocket: (url) => new FakeWebSocket(url),
      getCredentials: async () => ({ edgeUrl: "ws://localhost:8081/edge/ws", edgeToken: "edge-token" }),
      registry,
      onReady,
      random: () => 1,
    });
    return { registry, onReady, connection };
  }

  it("sends the auth frame as soon as the socket opens", async () => {
    const { connection } = setup();
    await connection.start();
    expect(FakeWebSocket.instances).toHaveLength(1);

    lastSocket().open();

    expect(lastSocket().sent).toEqual([JSON.stringify({ v: 1, type: "auth", token: "edge-token" })]);
  });

  it("starts heartbeat pings at the interval from the ready frame and reports origins", async () => {
    const { connection, onReady } = setup();
    await connection.start();
    expect(FakeWebSocket.instances).toHaveLength(1);
    const socket = lastSocket();
    socket.open();
    socket.receive({ type: "ready", heartbeatSec: 20, origins: ["https://app1.example.com"] });

    expect(onReady).toHaveBeenCalledWith(["https://app1.example.com"]);

    await vi.advanceTimersByTimeAsync(20_000);
    expect(socket.sent).toContain(JSON.stringify({ type: "ping" }));
  });

  it("resends app.up for every registered tab after a (re)connect", async () => {
    const { connection, registry } = setup();
    registry.register({ origin: "https://app1.example.com", appSession: "session-1", tabId: 1, send: vi.fn() });
    await connection.start();
    expect(FakeWebSocket.instances).toHaveLength(1);
    const socket = lastSocket();
    socket.open();
    socket.receive({ type: "ready", heartbeatSec: 20, origins: ["https://app1.example.com"] });

    expect(socket.sent).toContain(
      JSON.stringify({ type: "app.up", origin: "https://app1.example.com", appSession: "session-1" }),
    );
  });

  it("routes an incoming mcp frame to the matching registry entry", async () => {
    const { connection, registry } = setup();
    const send = vi.fn();
    registry.register({ origin: "https://app1.example.com", appSession: "session-1", tabId: 1, send });
    await connection.start();
    expect(FakeWebSocket.instances).toHaveLength(1);
    const socket = lastSocket();
    socket.open();
    socket.receive({ type: "ready", heartbeatSec: 20, origins: ["https://app1.example.com"] });

    socket.receive({
      type: "mcp",
      origin: "https://app1.example.com",
      appSession: "session-1",
      payload: { jsonrpc: "2.0", id: 1, method: "tools/list" },
    });

    expect(send).toHaveBeenCalledWith({ jsonrpc: "2.0", id: 1, method: "tools/list" });
  });

  it("sends an mcp frame built from the given origin/appSession/payload when connected", async () => {
    const { connection } = setup();
    await connection.start();
    expect(FakeWebSocket.instances).toHaveLength(1);
    const socket = lastSocket();
    socket.open();
    socket.receive({ type: "ready", heartbeatSec: 20, origins: ["https://app1.example.com"] });

    connection.sendMcpFrame("https://app1.example.com", "session-1", { jsonrpc: "2.0", id: 2, result: {} });

    expect(socket.sent).toContain(
      JSON.stringify({
        type: "mcp",
        origin: "https://app1.example.com",
        appSession: "session-1",
        payload: { jsonrpc: "2.0", id: 2, result: {} },
      }),
    );
  });

  it("reconnects with the computed backoff delay after the socket closes normally", async () => {
    const { connection } = setup();
    await connection.start();
    expect(FakeWebSocket.instances).toHaveLength(1);
    lastSocket().open();

    lastSocket().close(1006);
    expect(FakeWebSocket.instances).toHaveLength(1);

    // random() => 1 makes the first backoff delay exactly 1000ms (see backoff.test.ts).
    await vi.advanceTimersByTimeAsync(999);
    expect(FakeWebSocket.instances).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(FakeWebSocket.instances).toHaveLength(2);
  });

  it("does not reconnect after an auth failure close (4401)", async () => {
    const { connection } = setup();
    await connection.start();
    expect(FakeWebSocket.instances).toHaveLength(1);
    lastSocket().open();

    lastSocket().close(4401);

    await vi.advanceTimersByTimeAsync(60_000);
    expect(FakeWebSocket.instances).toHaveLength(1);
  });

  it("does nothing when no edge credentials are stored", async () => {
    const registry = createAppSessionRegistry();
    const connection = createEdgeConnection({
      connectSocket: (url) => new FakeWebSocket(url),
      getCredentials: async () => undefined,
      registry,
    });

    await connection.start();

    expect(FakeWebSocket.instances).toHaveLength(0);
  });
});
