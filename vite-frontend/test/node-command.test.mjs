import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { importTs } from "./import-ts.mjs";

const { isMissingPanelAddressError } = await importTs(
  new URL("../src/features/node/model.ts", import.meta.url),
);

describe("isMissingPanelAddressError", () => {
  it("detects backend messages for missing panel public address", () => {
    assert.equal(isMissingPanelAddressError("请先前往网站配置中设置ip"), true);
    assert.equal(isMissingPanelAddressError("请先设置面板公网地址"), true);
  });

  it("does not match unrelated command errors", () => {
    assert.equal(isMissingPanelAddressError("节点不存在"), false);
    assert.equal(isMissingPanelAddressError(""), false);
  });
});
