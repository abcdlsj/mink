import { Code, ConnectError } from "@connectrpc/connect";
import { describe, expect, it } from "vitest";
import { factErrorMessage } from "./facts";

describe("factErrorMessage", () => {
  it.each([Code.Unauthenticated, Code.PermissionDenied])(
    "maps %s to an honest authorization-required state",
    (code) => {
      expect(
        factErrorMessage(new ConnectError("denied", code), "set placement"),
      ).toBe(
        "Authorization required to set placement. Use an authenticated Human management client.",
      );
    },
  );

  it("keeps transport failure separate from authorization", () => {
    expect(
      factErrorMessage(
        new ConnectError("offline", Code.Unavailable),
        "load facts",
      ),
    ).toBe("Server unavailable. Check the connection and retry.");
  });
});
