import assert from "node:assert/strict";
import test from "node:test";

import { AGENT_PROFILES, createBrowserSessionHandoff } from "./dev-seed.mjs";

test("development seed defines three distinct Codex responsibilities", () => {
  assert.deepEqual(AGENT_PROFILES.map(({ name, handle, driver_kind }) => ({ name, handle, driver_kind })), [
    { name: "Coder", handle: "coder", driver_kind: "codex" },
    { name: "Reviewer", handle: "reviewer", driver_kind: "codex" },
    { name: "PM", handle: "pm", driver_kind: "codex" },
  ]);
  assert.equal(new Set(AGENT_PROFILES.map((profile) => profile.role_text)).size, 3);
  for (const profile of AGENT_PROFILES) assert.ok(profile.role_text.length > 80);
});

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
