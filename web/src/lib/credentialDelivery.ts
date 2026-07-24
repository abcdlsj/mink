import { xchacha20poly1305 } from "@noble/ciphers/chacha.js";
import { x25519 } from "@noble/curves/ed25519.js";
import { hkdf } from "@noble/hashes/hkdf.js";
import { sha256 } from "@noble/hashes/sha2.js";

const encoder = new TextEncoder();
const deliveryInfoPrefix = encoder.encode("sumi.credential.delivery.v1\0");
const maximumCredentialBytes = 64 * 1024;

export type CredentialDeliveryContext = {
  requestId: string;
  computerId: string;
  agentId: string;
  credentialKind: string;
  keyId: string;
  expiresAt: Date;
};

export type BrowserSealedCredential = {
  keyId: string;
  ephemeralPublicKey: Uint8Array;
  nonce: Uint8Array;
  ciphertext: Uint8Array;
};

export function sealCredential(
  publicKey: Uint8Array,
  context: CredentialDeliveryContext,
  rawCredential: string,
  randomBytes: (length: number) => Uint8Array = secureRandomBytes,
): BrowserSealedCredential {
  validateContext(context);
  if (publicKey.length !== 32) {
    throw new Error("Credential delivery public key must contain 32 bytes.");
  }

  const plaintext = encoder.encode(rawCredential);
  if (plaintext.length === 0 || plaintext.length > maximumCredentialBytes) {
    plaintext.fill(0);
    throw new Error("Credential must contain between 1 and 65536 bytes.");
  }

  const ephemeralSecret = exactRandomBytes(randomBytes, 32);
  const nonce = exactRandomBytes(randomBytes, 24);
  let shared: Uint8Array | undefined;
  let key: Uint8Array | undefined;
  try {
    const ephemeralPublicKey = x25519.getPublicKey(ephemeralSecret);
    shared = x25519.getSharedSecret(ephemeralSecret, publicKey);
    const associatedData = credentialDeliveryAssociatedData(context);
    const info = concatenate(deliveryInfoPrefix, associatedData);
    key = hkdf(sha256, shared, undefined, info, 32);
    const ciphertext = xchacha20poly1305(key, nonce, associatedData).encrypt(
      plaintext,
    );
    return {
      keyId: context.keyId,
      ephemeralPublicKey,
      nonce,
      ciphertext,
    };
  } finally {
    plaintext.fill(0);
    ephemeralSecret.fill(0);
    shared?.fill(0);
    key?.fill(0);
  }
}

export function credentialDeliveryAssociatedData(
  context: CredentialDeliveryContext,
): Uint8Array {
  validateContext(context);
  const values = [
    context.requestId,
    context.computerId,
    context.agentId,
    context.credentialKind,
    context.keyId,
  ].map((value) => encoder.encode(value));
  const size = values.reduce((total, value) => total + 4 + value.length, 8);
  const result = new Uint8Array(size);
  const view = new DataView(result.buffer);
  let offset = 0;
  for (const value of values) {
    view.setUint32(offset, value.length, false);
    offset += 4;
    result.set(value, offset);
    offset += value.length;
  }
  view.setBigUint64(
    offset,
    BigInt(Math.floor(context.expiresAt.getTime() / 1000)),
    false,
  );
  return result;
}

function validateContext(context: CredentialDeliveryContext) {
  const values = [
    context.requestId,
    context.computerId,
    context.agentId,
    context.credentialKind,
    context.keyId,
  ];
  if (values.some((value) => value.length === 0)) {
    throw new Error("Credential delivery context is incomplete.");
  }
  if (
    !Number.isFinite(context.expiresAt.getTime()) ||
    context.expiresAt.getTime() < 0
  ) {
    throw new Error("Credential delivery expiry is invalid.");
  }
}

function exactRandomBytes(
  randomBytes: (length: number) => Uint8Array,
  length: number,
) {
  const value = randomBytes(length);
  if (value.length !== length) {
    value.fill(0);
    throw new Error(
      `Random source returned ${value.length} bytes, want ${length}.`,
    );
  }
  return value;
}

function secureRandomBytes(length: number) {
  return crypto.getRandomValues(new Uint8Array(length));
}

function concatenate(left: Uint8Array, right: Uint8Array) {
  const result = new Uint8Array(left.length + right.length);
  result.set(left);
  result.set(right, left.length);
  return result;
}
