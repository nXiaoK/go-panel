import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
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
  return import(`data:text/javascript;charset=utf-8,${encodeURIComponent(compiled)}`);
}

const {
  buildR2BackupUpdate,
  defaultR2BackupForm,
  formatR2BackupBytes,
  formatR2BackupTimestamp,
  isR2BackupFormDirty,
  r2BackupSettingsToForm,
  toggleR2BackupSecretClear,
} = await importTs(new URL("../src/lib/r2-backup.ts", import.meta.url));

const settings = {
  enabled: false,
  accountId: "0123456789abcdef0123456789abcdef",
  accessKeyId: "access-id",
  bucket: "panel-backups",
  objectPrefix: "flux-panel/backups",
  scheduleTime: "03:00",
  retentionCount: 30,
  secretConfigured: true,
  secretUsable: true,
  credentialEncryptionAvailable: true,
  lastAttemptAt: 0,
  lastSuccessAt: 0,
  lastSize: 0,
};

describe("R2 backup form helpers", () => {
  it("keeps frontend defaults aligned with the safe backend defaults", () => {
    assert.equal(defaultR2BackupForm.enabled, false);
    assert.equal(defaultR2BackupForm.objectPrefix, "flux-panel/backups");
    assert.equal(defaultR2BackupForm.scheduleTime, "03:00");
    assert.equal(defaultR2BackupForm.retentionCount, "30");
  });

  it("maps the redacted response without inventing a secret", () => {
    const form = r2BackupSettingsToForm(settings);
    assert.equal(form.accountId, settings.accountId);
    assert.equal(form.retentionCount, "30");
    assert.equal("secretAccessKey" in form, false);
    assert.equal(isR2BackupFormDirty(settings, form, "", false), false);
    assert.equal(isR2BackupFormDirty(settings, form, "rotated-secret", false), true);
    assert.equal(isR2BackupFormDirty(settings, { ...form, enabled: true }, "", false), true);
  });

  it("drops an unsaved replacement when switching to clear-secret mode", () => {
    assert.deepEqual(toggleR2BackupSecretClear(false, "unsaved-secret"), {
      clearSecret: true,
      secretAccessKey: "",
    });
    assert.deepEqual(toggleR2BackupSecretClear(true, ""), {
      clearSecret: false,
      secretAccessKey: "",
    });
  });

  it("builds strict update payloads and rejects unsafe clear or partial integers", () => {
    const form = r2BackupSettingsToForm(settings);
    assert.equal(
      buildR2BackupUpdate({ ...form, retentionCount: "2x" }, "", false).error,
      "备份保留数量必须是整数",
    );
    assert.equal(
      buildR2BackupUpdate({ ...form, enabled: true }, "", true).error,
      "清除密钥前请先关闭自动备份",
    );
    assert.equal(
      buildR2BackupUpdate(form, "replacement-secret", true).error,
      "不能同时填写新密钥并清除密钥",
    );

    const built = buildR2BackupUpdate(form, "new-secret", false);
    assert.deepEqual(built.value, {
      ...form,
      retentionCount: 30,
      secretAccessKey: "new-secret",
      clearSecret: false,
    });
  });

  it("formats status values for the settings card", () => {
    assert.equal(formatR2BackupTimestamp(0), "尚无记录");
    assert.equal(formatR2BackupBytes(0), "0 B");
    assert.equal(formatR2BackupBytes(1024), "1.00 KB");
    assert.equal(formatR2BackupBytes(1024 * 1024), "1.00 MB");
  });
});
