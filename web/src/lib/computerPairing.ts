export const computerPairingFilename = "sumi-computer-pairing.json";
export const computerPairingCommand = `chmod 600 "$HOME/Downloads/${computerPairingFilename}" && \\
sumi computer pair join --file "$HOME/Downloads/${computerPairingFilename}" && \\
sumi computer start`;

export type PairingServerIdentity = {
  kind: "literal-loopback" | "system-trust";
};

export type ComputerPairingBundle = {
  version: 1;
  request_id: string;
  server_origin: string;
  server_identity: PairingServerIdentity;
  pairing_token: string;
  expires_at: string;
};

export type PreparedComputerPairing = {
  requestId: string;
  pairingToken: string;
  expiresAt: Date;
  bundle: ComputerPairingBundle;
};

export class UnsafePairingOriginError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "UnsafePairingOriginError";
  }
}

export function prepareComputerPairing(
  rawOrigin: string,
  now = new Date(),
  randomBytes?: Uint8Array,
): PreparedComputerPairing {
  const endpoint = getPairingEndpoint(rawOrigin);
  const requestId = crypto.randomUUID();
  const bytes = randomBytes ?? crypto.getRandomValues(new Uint8Array(32));
  if (bytes.byteLength !== 32) {
    throw new Error("Pairing token entropy is unavailable.");
  }
  const pairingToken = rawBase64URL(bytes);
  const expiresAt = new Date(now.getTime() + 10 * 60 * 1000);
  return {
    requestId,
    pairingToken,
    expiresAt,
    bundle: {
      version: 1,
      request_id: requestId,
      server_origin: endpoint.origin,
      server_identity: { kind: endpoint.identity },
      pairing_token: pairingToken,
      expires_at: expiresAt.toISOString(),
    },
  };
}

export function serializeComputerPairingBundle(
  bundle: ComputerPairingBundle,
): string {
  return `${JSON.stringify(bundle, null, 2)}\n`;
}

export function downloadComputerPairingBundle(
  bundle: ComputerPairingBundle,
): void {
  const payload = serializeComputerPairingBundle(bundle);
  const url = URL.createObjectURL(
    new Blob([payload], { type: "application/json" }),
  );
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = computerPairingFilename;
  anchor.hidden = true;
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}

export function getPairingEndpoint(rawOrigin: string): {
  origin: string;
  identity: PairingServerIdentity["kind"];
} {
  let parsed: URL;
  try {
    parsed = new URL(rawOrigin);
  } catch {
    throw new UnsafePairingOriginError("This Sumi address is invalid.");
  }
  if (
    parsed.username ||
    parsed.password ||
    parsed.pathname !== "/" ||
    parsed.search ||
    parsed.hash
  ) {
    throw new UnsafePairingOriginError(
      "Pairing requires a Server origin without credentials, path, query, or fragment.",
    );
  }
  if (parsed.protocol === "https:") {
    return { origin: parsed.origin, identity: "system-trust" };
  }
  if (
    parsed.protocol === "http:" &&
    (isIPv4Loopback(parsed.hostname) || parsed.hostname === "[::1]")
  ) {
    return { origin: parsed.origin, identity: "literal-loopback" };
  }
  throw new UnsafePairingOriginError(
    "Computer pairing needs literal 127.0.0.1 HTTP or system-trusted HTTPS. Remote HTTP is intentionally blocked.",
  );
}

function isIPv4Loopback(hostname: string): boolean {
  const match = /^127\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(hostname);
  return !!match && match.slice(1).every((part) => Number(part) <= 255);
}

function rawBase64URL(bytes: Uint8Array): string {
  const encoded = btoa(String.fromCharCode(...bytes));
  return encoded.replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
}
