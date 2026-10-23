import { readdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { brotliCompress, constants, gzip } from "node:zlib";

const gzipAsync = promisify(gzip);
const brotliAsync = promisify(brotliCompress);

// 构建时为 JS/CSS/SVG 生成旁路压缩文件；保留原文件供不支持压缩的客户端回退。
// gzip level 9、Brotli quality 11 仅增加构建成本，运行时不再压缩请求。
export async function precompress(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      await precompress(path);
    } else if (entry.isFile() && /\.(js|css|svg)$/.test(entry.name)) {
      const source = await readFile(path);
      const [gz, br] = await Promise.all([
        gzipAsync(source, { level: 9 }),
        brotliAsync(source, { params: { [constants.BROTLI_PARAM_QUALITY]: 11 } }),
      ]);
      await Promise.all([writeFile(`${path}.gz`, gz), writeFile(`${path}.br`, br)]);
    }
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await precompress(fileURLToPath(new URL("../dist/assets", import.meta.url)));
}
