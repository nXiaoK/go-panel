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
  for (const match of compiled.matchAll(/(?:from\s*|import\s*)(["'])(\.[^"']+)\1/g)) {
    const target = new URL(match[2].endsWith(".ts") ? match[2] : `${match[2]}.ts`, path);
    compiled = compiled.replace(
      match[1] + match[2] + match[1],
      JSON.stringify(await sourceUrl(target)),
    );
  }
  return `data:text/javascript;charset=utf-8,${encodeURIComponent(compiled)}`;
}
