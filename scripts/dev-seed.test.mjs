import assert from "node:assert/strict";
import test from "node:test";

import { createBrowserSessionHandoff } from "./dev-seed.mjs";

test("browser Session handoff sets the Cookie and remains retryable", async (context) => {
  const destination = "http://127.0.0.1:5173/s/dev-space/channels/general";
  const handoff = await createBrowserSessionHandoff("sumi_session=test-token", destination);
  context.after(() => handoff.close());

  const response = await fetch(handoff.url, { redirect: "manual" });
  assert.equal(response.status, 302);
  assert.equal(response.headers.get("location"), destination);
  assert.match(
    response.headers.get("set-cookie") ?? "",
    /^sumi_session=test-token; Path=\/; HttpOnly; SameSite=Lax$/,
  );
  assert.equal(response.headers.get("cache-control"), "no-store");

  const replay = await fetch(handoff.url, { redirect: "manual" });
  assert.equal(replay.status, 302);
  assert.equal(replay.headers.get("location"), destination);
  assert.match(replay.headers.get("set-cookie") ?? "", /^sumi_session=test-token;/);
});
