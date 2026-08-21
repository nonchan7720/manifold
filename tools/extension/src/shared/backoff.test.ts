import { describe, expect, it } from "vitest";
import { computeReconnectDelayMs } from "./backoff";

describe("computeReconnectDelayMs", () => {
  it("starts at 1s (jittered) for the first attempt", () => {
    expect(computeReconnectDelayMs(0, () => 0)).toBe(500);
    expect(computeReconnectDelayMs(0, () => 1)).toBe(1000);
  });

  it("doubles the base delay for each subsequent attempt", () => {
    expect(computeReconnectDelayMs(1, () => 0)).toBe(1000);
    expect(computeReconnectDelayMs(1, () => 1)).toBe(2000);
    expect(computeReconnectDelayMs(2, () => 0)).toBe(2000);
    expect(computeReconnectDelayMs(2, () => 1)).toBe(4000);
  });

  it("caps the base delay at 30s regardless of how large the attempt is", () => {
    expect(computeReconnectDelayMs(10, () => 0)).toBe(15000);
    expect(computeReconnectDelayMs(10, () => 1)).toBe(30000);
    expect(computeReconnectDelayMs(100, () => 1)).toBe(30000);
  });

  it("uses Math.random by default and stays within the expected range", () => {
    for (let attempt = 0; attempt < 8; attempt++) {
      const delay = computeReconnectDelayMs(attempt);
      const base = Math.min(1000 * 2 ** attempt, 30000);
      expect(delay).toBeGreaterThanOrEqual(base / 2);
      expect(delay).toBeLessThanOrEqual(base);
    }
  });
});
