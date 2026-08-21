import { describe, expect, it } from "vitest";
import {
  buildAppDownFrame,
  buildAppUpFrame,
  buildAuthFrame,
  buildMcpFrame,
  buildPingFrame,
  parseIncomingFrame,
} from "./envelope";

describe("envelope builders", () => {
  it("builds an auth frame with protocol version 1", () => {
    expect(buildAuthFrame("edge-token")).toEqual({
      v: 1,
      type: "auth",
      token: "edge-token",
    });
  });

  it("builds an app.up frame", () => {
    expect(buildAppUpFrame("https://app1.example.com", "session-1")).toEqual({
      type: "app.up",
      origin: "https://app1.example.com",
      appSession: "session-1",
    });
  });

  it("builds an app.down frame", () => {
    expect(buildAppDownFrame("https://app1.example.com", "session-1")).toEqual({
      type: "app.down",
      origin: "https://app1.example.com",
      appSession: "session-1",
    });
  });

  it("builds an mcp frame carrying the payload untouched", () => {
    const payload = { jsonrpc: "2.0", id: 1, method: "tools/list" };
    expect(buildMcpFrame("https://app1.example.com", "session-1", payload)).toEqual({
      type: "mcp",
      origin: "https://app1.example.com",
      appSession: "session-1",
      payload,
    });
  });

  it("builds a ping frame", () => {
    expect(buildPingFrame()).toEqual({ type: "ping" });
  });
});

describe("parseIncomingFrame", () => {
  it("parses a ready frame", () => {
    const raw = JSON.stringify({
      type: "ready",
      heartbeatSec: 20,
      origins: ["https://app1.example.com"],
    });
    expect(parseIncomingFrame(raw)).toEqual({
      type: "ready",
      heartbeatSec: 20,
      origins: ["https://app1.example.com"],
    });
  });

  it("parses an mcp frame", () => {
    const raw = JSON.stringify({
      type: "mcp",
      origin: "https://app1.example.com",
      appSession: "session-1",
      payload: { jsonrpc: "2.0", id: 2, result: {} },
    });
    expect(parseIncomingFrame(raw)).toEqual({
      type: "mcp",
      origin: "https://app1.example.com",
      appSession: "session-1",
      payload: { jsonrpc: "2.0", id: 2, result: {} },
    });
  });

  it("parses a pong frame", () => {
    expect(parseIncomingFrame(JSON.stringify({ type: "pong" }))).toEqual({ type: "pong" });
  });

  it("parses an error frame", () => {
    const raw = JSON.stringify({ type: "error", message: "origin not allowed" });
    expect(parseIncomingFrame(raw)).toEqual({ type: "error", message: "origin not allowed" });
  });

  it("returns null for malformed JSON", () => {
    expect(parseIncomingFrame("not json")).toBeNull();
  });

  it("returns null for an unknown frame type", () => {
    expect(parseIncomingFrame(JSON.stringify({ type: "close" }))).toBeNull();
  });

  it("returns null for a frame missing required fields", () => {
    expect(parseIncomingFrame(JSON.stringify({ type: "ready" }))).toBeNull();
  });
});
