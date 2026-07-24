import { xchacha20poly1305 } from "@noble/ciphers/chacha.js";
import { x25519 } from "@noble/curves/ed25519.js";
import { hkdf } from "@noble/hashes/hkdf.js";
import { sha256 } from "@noble/hashes/sha2.js";
import { describe, expect, it } from "vitest";
import {
  credentialDeliveryAssociatedData,
  sealCredential,
  type CredentialDeliveryContext,
} from "./credentialDelivery";

const encoder = new TextEncoder();

describe("sealCredential", () => {
  it("uses the exact Go delivery context encoding and opens the sealed value", () => {
    const serverSecret = bytes(1, 32);
    const ephemeralSecret = bytes(33, 32);
    const nonce = bytes(65, 24);
    const publicKey = x25519.getPublicKey(serverSecret);
    const context: CredentialDeliveryContext = {
      requestId: "11111111-1111-4111-8111-111111111111",
      computerId: "22222222-2222-4222-8222-222222222222",
      agentId: "33333333-3333-4333-8333-333333333333",
      credentialKind: "openai",
      keyId: "44444444-4444-4444-8444-444444444444",
      expiresAt: new Date("2026-07-24T12:34:56Z"),
    };
    const randomValues = [ephemeralSecret, nonce];
    const sealed = sealCredential(
      publicKey,
      context,
      "sk-browser-only",
      (length) => {
        const value = randomValues.shift();
        if (!value || value.length !== length) throw new Error("bad fixture");
        return value.slice();
      },
    );

    expect(hex(sealed.ephemeralPublicKey)).toBe(
      "5869aff450549732cbaaed5e5df9b30a6da31cb0e5742bad5ad4a1a768f1a67b",
    );
    expect(hex(sealed.nonce)).toBe(
      "4142434445464748494a4b4c4d4e4f505152535455565758",
    );
    expect(hex(sealed.ciphertext)).toBe(
      "fe05d66ced0274b657cd10154777ecd58c1abdb08f1de0a5ce5c306ec8d2cf",
    );

    const associatedData = credentialDeliveryAssociatedData(context);
    const shared = x25519.getSharedSecret(
      serverSecret,
      sealed.ephemeralPublicKey,
    );
    const info = concatenate(
      encoder.encode("sumi.credential.delivery.v1\0"),
      associatedData,
    );
    const key = hkdf(sha256, shared, undefined, info, 32);
    const plaintext = xchacha20poly1305(
      key,
      sealed.nonce,
      associatedData,
    ).decrypt(sealed.ciphertext);

    expect(new TextDecoder().decode(plaintext)).toBe("sk-browser-only");
    expect(hex(associatedData).endsWith("000000006a635bf0")).toBe(true);
  });

  it("binds authentication to the complete delivery context", () => {
    const serverSecret = bytes(1, 32);
    const publicKey = x25519.getPublicKey(serverSecret);
    const context: CredentialDeliveryContext = {
      requestId: "request",
      computerId: "computer",
      agentId: "agent",
      credentialKind: "anthropic",
      keyId: "key",
      expiresAt: new Date("2026-07-24T12:34:56Z"),
    };
    const sealed = sealCredential(publicKey, context, "secret");
    const changed = { ...context, agentId: "another-agent" };
    const associatedData = credentialDeliveryAssociatedData(changed);
    const shared = x25519.getSharedSecret(
      serverSecret,
      sealed.ephemeralPublicKey,
    );
    const key = hkdf(
      sha256,
      shared,
      undefined,
      concatenate(
        encoder.encode("sumi.credential.delivery.v1\0"),
        associatedData,
      ),
      32,
    );

    expect(() =>
      xchacha20poly1305(key, sealed.nonce, associatedData).decrypt(
        sealed.ciphertext,
      ),
    ).toThrow();
  });
});

function bytes(start: number, length: number) {
  return Uint8Array.from({ length }, (_, index) => start + index);
}

function hex(value: Uint8Array) {
  return Array.from(value, (byte) => byte.toString(16).padStart(2, "0")).join(
    "",
  );
}

function concatenate(left: Uint8Array, right: Uint8Array) {
  const result = new Uint8Array(left.length + right.length);
  result.set(left);
  result.set(right, left.length);
  return result;
}
