import { describe, expect, it } from "vitest";
import {
  computerPairingCommand,
  prepareComputerPairing,
  serializeComputerPairingBundle,
  UnsafePairingOriginError,
} from "./computerPairing";

describe("Computer pairing bundle", () => {
  it("creates the canonical v1 bundle without putting the token in the command", () => {
    const now = new Date("2026-07-24T01:02:03.000Z");
    const prepared = prepareComputerPairing(
      "https://SUMI.EXAMPLE:443",
      now,
      new Uint8Array(32).fill(7),
    );

    expect(prepared.requestId).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
    expect(prepared.pairingToken).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(prepared.expiresAt.toISOString()).toBe("2026-07-24T01:12:03.000Z");
    expect(prepared.bundle).toEqual({
      version: 1,
      request_id: prepared.requestId,
      server_origin: "https://sumi.example",
      server_identity: { kind: "system-trust" },
      pairing_token: prepared.pairingToken,
      expires_at: "2026-07-24T01:12:03.000Z",
    });
    expect(computerPairingCommand).toContain("chmod 600");
    expect(computerPairingCommand).toContain("sumi computer pair join --file");
    expect(computerPairingCommand).toContain("sumi computer start");
    expect(computerPairingCommand).not.toContain(prepared.pairingToken);
  });

  it("uses literal-loopback identity only for literal loopback HTTP", () => {
    const prepared = prepareComputerPairing(
      "http://127.0.0.1:8080",
      new Date(0),
      new Uint8Array(32),
    );
    expect(prepared.bundle.server_origin).toBe("http://127.0.0.1:8080");
    expect(prepared.bundle.server_identity).toEqual({
      kind: "literal-loopback",
    });
  });

  it.each([
    "http://localhost:8080",
    "http://100.82.34.26:18080",
    "ftp://127.0.0.1",
    "https://user@example.com",
    "https://example.com/path",
    "https://example.com?token=secret",
  ])("fails closed for unsafe origin %s", (origin) => {
    expect(() =>
      prepareComputerPairing(origin, new Date(0), new Uint8Array(32)),
    ).toThrow(UnsafePairingOriginError);
  });

  it("serializes a newline-terminated JSON bundle", () => {
    const bundle = prepareComputerPairing(
      "https://sumi.example",
      new Date(0),
      new Uint8Array(32).fill(1),
    ).bundle;
    const payload = serializeComputerPairingBundle(bundle);
    expect(payload.endsWith("\n")).toBe(true);
    expect(JSON.parse(payload)).toEqual(bundle);
  });
});
