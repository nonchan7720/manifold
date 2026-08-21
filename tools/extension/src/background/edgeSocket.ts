import {
  buildAppDownFrame,
  buildAppUpFrame,
  buildAuthFrame,
  buildMcpFrame,
  buildPingFrame,
  parseIncomingFrame,
} from "../shared/envelope";
import type { AppSessionRegistry } from "./appSessionRegistry";
import { computeReconnectDelayMs } from "../shared/backoff";

const WS_OPEN = 1;
/** close(4401): edge token rejected or missing — retrying with the same token cannot help. */
const AUTH_FAILURE_CLOSE_CODE = 4401;

export interface WebSocketLike {
  readyState: number;
  send: (data: string) => void;
  close: (code?: number) => void;
  onopen: (() => void) | null;
  onmessage: ((event: { data: string }) => void) | null;
  onclose: ((event: { code: number }) => void) | null;
  onerror: ((event: unknown) => void) | null;
}

export interface EdgeCredentials {
  edgeUrl: string;
  edgeToken: string;
}

export type EdgeConnectionStatus = "connecting" | "ready" | "reconnecting" | "closed";

export interface EdgeConnectionDeps {
  connectSocket: (url: string) => WebSocketLike;
  getCredentials: () => Promise<EdgeCredentials | undefined>;
  registry: AppSessionRegistry;
  onReady?: (origins: string[]) => void;
  onStatusChange?: (status: EdgeConnectionStatus) => void;
  random?: () => number;
}

export interface EdgeConnection {
  start: () => Promise<void>;
  stop: () => void;
  sendMcpFrame: (origin: string, appSession: string, payload: unknown) => void;
  sendAppUp: (origin: string, appSession: string) => void;
  sendAppDown: (origin: string, appSession: string) => void;
}

/**
 * Owns the single WebSocket connection to a reverse gateway edge endpoint:
 * first-message auth, heartbeat, reconnect with backoff, and dispatch of
 * incoming mcp frames to the tab that owns their appSession. See
 * docs/design/webmcp-reverse-gateway.ja.md ("Edge WebSocket プロトコル").
 */
export function createEdgeConnection(deps: EdgeConnectionDeps): EdgeConnection {
  let socket: WebSocketLike | undefined;
  let heartbeatTimer: ReturnType<typeof setInterval> | undefined;
  let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
  let reconnectAttempt = 0;
  let stopped = false;
  let awaitingPong = false;

  function setStatus(status: EdgeConnectionStatus) {
    deps.onStatusChange?.(status);
  }

  function clearHeartbeat() {
    if (heartbeatTimer !== undefined) {
      clearInterval(heartbeatTimer);
      heartbeatTimer = undefined;
    }
    awaitingPong = false;
  }

  function send(frame: object) {
    if (socket && socket.readyState === WS_OPEN) {
      socket.send(JSON.stringify(frame));
    }
  }

  function startHeartbeat(heartbeatSec: number) {
    clearHeartbeat();
    heartbeatTimer = setInterval(() => {
      // A missed pong means the connection is stale (the design notes MV3
      // service workers can be suspended mid-connection); force a reconnect
      // instead of waiting for the OS/browser to notice.
      if (awaitingPong) {
        socket?.close();
        return;
      }
      awaitingPong = true;
      send(buildPingFrame());
    }, heartbeatSec * 1000);
  }

  function handleReady(origins: string[]) {
    reconnectAttempt = 0;
    setStatus("ready");
    deps.onReady?.(origins);
    for (const entry of deps.registry.list()) {
      send(buildAppUpFrame(entry.origin, entry.appSession));
    }
  }

  function handleMessage(raw: string) {
    const frame = parseIncomingFrame(raw);
    if (!frame) return;

    switch (frame.type) {
      case "ready":
        startHeartbeat(frame.heartbeatSec);
        handleReady(frame.origins);
        break;
      case "mcp": {
        const entry = deps.registry.get(frame.appSession);
        if (entry && entry.origin === frame.origin) {
          entry.send(frame.payload);
        }
        break;
      }
      case "pong":
        awaitingPong = false;
        break;
      case "error":
        break;
    }
  }

  function scheduleReconnect() {
    clearHeartbeat();
    if (stopped) return;
    setStatus("reconnecting");
    const delay = computeReconnectDelayMs(reconnectAttempt, deps.random);
    reconnectAttempt += 1;
    reconnectTimer = setTimeout(() => {
      void connect();
    }, delay);
  }

  async function connect(): Promise<void> {
    const credentials = await deps.getCredentials();
    if (!credentials) {
      setStatus("closed");
      return;
    }

    setStatus("connecting");
    const ws = deps.connectSocket(credentials.edgeUrl);
    socket = ws;
    ws.onopen = () => send(buildAuthFrame(credentials.edgeToken));
    ws.onmessage = (event) => handleMessage(event.data);
    ws.onclose = (event) => {
      socket = undefined;
      if (event.code === AUTH_FAILURE_CLOSE_CODE) {
        clearHeartbeat();
        setStatus("closed");
        return;
      }
      scheduleReconnect();
    };
    ws.onerror = () => {
      // onclose always follows onerror for WebSocket; reconnect is handled there.
    };
  }

  return {
    async start() {
      stopped = false;
      await connect();
    },
    stop() {
      stopped = true;
      clearHeartbeat();
      if (reconnectTimer !== undefined) {
        clearTimeout(reconnectTimer);
        reconnectTimer = undefined;
      }
      socket?.close();
      socket = undefined;
      setStatus("closed");
    },
    sendMcpFrame(origin, appSession, payload) {
      send(buildMcpFrame(origin, appSession, payload));
    },
    sendAppUp(origin, appSession) {
      send(buildAppUpFrame(origin, appSession));
    },
    sendAppDown(origin, appSession) {
      send(buildAppDownFrame(origin, appSession));
    },
  };
}
