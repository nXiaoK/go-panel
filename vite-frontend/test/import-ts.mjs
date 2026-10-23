import { readFile } from "node:fs/promises";
import ts from "typescript";

export async function importTs(path) {
  return import(await sourceUrl(path));
}
async function sourceUrl(path) {
  const source = await readFile(path, "utf8");
  let compiled = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.ES2022,
      target: ts.ScriptTarget.ES2022,
      verbatimModuleSyntax: false,
    },
  }).outputText;
  for (const match of compiled.matchAll(/(?:from\s*|import\s*)(["'])((?:\.|@\/)[^"']+)\1/g)) {
    const relative = match[2].startsWith("@/")
      ? new URL(`../src/${match[2].slice(2)}`, import.meta.url).href
      : match[2];
    const target = new URL(relative.endsWith(".ts") ? relative : `${relative}.ts`, path);
    compiled = compiled.replace(
      match[1] + match[2] + match[1],
      JSON.stringify(await sourceUrl(target)),
    );
  }
  return `data:text/javascript;charset=utf-8,${encodeURIComponent(compiled)}`;
}
