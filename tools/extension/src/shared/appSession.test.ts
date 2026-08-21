import { afterEach, describe, expect, it, vi } from "vitest";
import { generateAppSession } from "./appSession";

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

describe("generateAppSession", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns a v4 UUID", () => {
    expect(generateAppSession()).toMatch(UUID_PATTERN);
  });

  it("returns a different value on each call", () => {
    expect(generateAppSession()).not.toBe(generateAppSession());
  });

  it("still works when crypto.randomUUID is undefined, as on an insecure context (http on a non-localhost origin)", () => {
    const original = crypto.randomUUID;
    // @ts-expect-error -- simulating an insecure context, where Chromium
    // doesn't expose crypto.randomUUID at all (see payable.lvh.me:4456).
    crypto.randomUUID = undefined;

    try {
      expect(generateAppSession()).toMatch(UUID_PATTERN);
    } finally {
      crypto.randomUUID = original;
    }
  });
});
