import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import ts from "typescript";

async function importTs(path) {
  const source = await readFile(path, "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.ES2022,
      target: ts.ScriptTarget.ES2022,
      verbatimModuleSyntax: false,
    },
  }).outputText;
  const encoded = encodeURIComponent(compiled);
  return import(`data:text/javascript;charset=utf-8,${encoded}`);
}

const {
  allowInsecureNodeDownloadsEnvOverrideName,
  normalizeConfigItems,
  keyConfigMeta,
  keyConfigNames,
  readOnlyConfigNames,
  resolveInsecureDownloadNoticeState,
} = await importTs(new URL("../src/lib/config-items.ts", import.meta.url));

describe("normalizeConfigItems", () => {
  it("adds important site settings even when backend config list is empty", () => {
    const items = normalizeConfigItems({});
    const names = items.map((item) => item.name);

    assert.deepEqual(names.slice(0, keyConfigNames.length), keyConfigNames);
    assert.equal(items.find((item) => item.name === "ip")?.value, "");
    assert.equal(
      items.find((item) => item.name === "allow_insecure_node_downloads")?.value,
      "false",
    );
    assert.equal(
      items.find((item) => item.name === allowInsecureNodeDownloadsEnvOverrideName)?.value,
      "false",
    );
    assert.deepEqual(readOnlyConfigNames, [allowInsecureNodeDownloadsEnvOverrideName]);
    const unsafeDownloads = keyConfigMeta.find(
      (item) => item.name === "allow_insecure_node_downloads",
    );
    assert.equal(unsafeDownloads?.type, "switch");
    assert.match(unsafeDownloads?.description ?? "", /明文 HTTP.*泄露.*篡改/);
  });

  it("keeps backend values, hides unsupported captcha keys, and appends unknown config keys", () => {
    const items = normalizeConfigItems({
      captcha_enabled: "true",
      captcha_type: "RANDOM",
      ip: "panel.example.com:6365",
      allow_insecure_node_downloads: "true",
      allow_insecure_node_downloads_env_override: "true",
      custom_key: "custom value",
    });

    assert.equal(items.find((item) => item.name === "ip")?.value, "panel.example.com:6365");
    assert.equal(
      items.find((item) => item.name === "allow_insecure_node_downloads")?.value,
      "true",
    );
    assert.equal(
      items.find((item) => item.name === allowInsecureNodeDownloadsEnvOverrideName)?.value,
      "true",
    );
    assert.equal(
      items.filter((item) => item.name === allowInsecureNodeDownloadsEnvOverrideName).length,
      1,
    );
    assert.equal(
      items.find((item) => item.name === "captcha_enabled"),
      undefined,
    );
    assert.equal(
      items.find((item) => item.name === "captcha_type"),
      undefined,
    );
    assert.equal(items.at(-1)?.name, "custom_key");
    assert.equal(items.at(-1)?.value, "custom value");
  });
});

describe("resolveInsecureDownloadNoticeState", () => {
  it("keeps environment overrides visible regardless of the form draft", () => {
    assert.equal(
      resolveInsecureDownloadNoticeState("false", "false", "true", false),
      "environment_override",
    );
    assert.equal(
      resolveInsecureDownloadNoticeState("true", "false", "true", true),
      "environment_override",
    );
  });

  it("distinguishes persisted policy from unsaved enable and disable drafts", () => {
    assert.equal(resolveInsecureDownloadNoticeState("true", "true", "false", false), "enabled");
    assert.equal(
      resolveInsecureDownloadNoticeState("true", "false", "false", true),
      "disable_pending",
    );
    assert.equal(
      resolveInsecureDownloadNoticeState("false", "true", "false", true),
      "enable_pending",
    );
    assert.equal(resolveInsecureDownloadNoticeState("false", "false", "false", false), null);
  });
});
