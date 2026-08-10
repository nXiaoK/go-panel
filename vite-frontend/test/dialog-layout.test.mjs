import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

const dashboardSource = await readFile(
  new URL("../src/routes/_app.index.tsx", import.meta.url),
  "utf8",
);

function getStressDialogClasses() {
  const dialogMatch = dashboardSource.match(
    /<DialogContent className="([^"]+)">\s*<DialogHeader[^>]*>\s*<DialogTitle>隧道压力测试<\/DialogTitle>/s,
  );
  const bodyMatch = dashboardSource.match(
    /<DialogTitle>隧道压力测试<\/DialogTitle>\s*<\/DialogHeader>\s*<div className="([^"]+)">/s,
  );

  assert.ok(dialogMatch, "stress test dialog content className should be discoverable");
  assert.ok(bodyMatch, "stress test dialog body className should be discoverable");

  return {
    content: dialogMatch[1],
    body: bodyMatch[1],
  };
}

describe("dialog layout", () => {
  it("keeps pressure test results scrollable without pushing footer actions out of view", () => {
    const classes = getStressDialogClasses();

    assert.match(classes.content, /\bmax-h-\[calc\(100dvh-2rem\)\]/);
    assert.match(classes.content, /\bflex\b/);
    assert.match(classes.content, /\bflex-col\b/);
    assert.match(classes.content, /\boverflow-hidden\b/);
    assert.doesNotMatch(classes.content, /\bgrid-rows-/);

    assert.match(classes.body, /\bmin-h-0\b/);
    assert.match(classes.body, /\bflex-1\b/);
    assert.match(classes.body, /\boverflow-y-auto\b/);
  });
});
