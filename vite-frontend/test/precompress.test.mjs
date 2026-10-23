import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { brotliDecompressSync, gunzipSync } from "node:zlib";
import { precompress } from "../scripts/precompress.mjs";

test("precompression preserves originals and creates valid gzip/Brotli recursively", async () => {
  const directory = await mkdtemp(join(tmpdir(), "go-panel-precompress-"));
  try {
    await mkdir(join(directory, "nested"));
    for (const name of ["app.js", "style.css", "nested/icon.svg", "ignored.png"]) {
      await writeFile(join(directory, name), `source of ${name}`);
    }
    await precompress(directory);
    for (const name of ["app.js", "style.css", "nested/icon.svg"]) {
      const original = await readFile(join(directory, name));
      assert.equal(original.toString(), `source of ${name}`);
      assert.deepEqual(gunzipSync(await readFile(join(directory, `${name}.gz`))), original);
      assert.deepEqual(
        brotliDecompressSync(await readFile(join(directory, `${name}.br`))),
        original,
      );
    }
    assert.equal(
      (await readdir(directory)).some((name) => name.startsWith("ignored.png.")),
      false,
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
