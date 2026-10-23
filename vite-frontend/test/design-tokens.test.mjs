import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { join, relative } from "node:path";
import test from "node:test";
import ts from "typescript";
import { legacyViolations } from "./design-token-legacy.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
const sourceRoot = join(root, "src");

// 设计门禁配置：按字面值计数，而非整文件豁免；扫描变量/模板片段以覆盖动态 className。
const forbidden =
  /(?:emerald|amber|blue|indigo|purple|cyan|green|red|yellow|orange)-\d{2,3}|shadow-glow|bg-gradient-primary|text-gradient-primary|bg-grid|text-\[(?:9|10|11)px\]/g;
function violations(source) {
  const result = {};
  const tree = ts.createSourceFile(
    "view.tsx",
    source,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  );
  // 使用 TypeScript 词法绑定解析常量，内层同名变量不会覆盖外层 className 的来源。
  const program = ts.createProgram(
    [tree.fileName],
    { noLib: true, noResolve: true, target: ts.ScriptTarget.Latest, jsx: ts.JsxEmit.Preserve },
    {
      getSourceFile: (name) => (name === tree.fileName ? tree : undefined),
      getDefaultLibFileName: () => "",
      writeFile: () => {},
      getCurrentDirectory: () => "",
      getDirectories: () => [],
      fileExists: (name) => name === tree.fileName,
      readFile: () => undefined,
      getCanonicalFileName: (name) => name,
      useCaseSensitiveFileNames: () => true,
      getNewLine: () => "\n",
    },
  );
  const checker = program.getTypeChecker();
  // 只折叠源码可证明的常量，覆盖被拆开的完整类名，不执行任意项目代码。
  function staticText(node, seen = new Set()) {
    if (ts.isStringLiteralLike(node)) return node.text;
    if (ts.isNumericLiteral(node)) return Number(node.text);
    if (ts.isParenthesizedExpression(node)) return staticText(node.expression, seen);
    if (ts.isIdentifier(node)) {
      const declaration = checker.getSymbolAtLocation(node)?.valueDeclaration;
      if (
        declaration &&
        ts.isVariableDeclaration(declaration) &&
        declaration.initializer &&
        ts.isVariableDeclarationList(declaration.parent) &&
        declaration.parent.flags & ts.NodeFlags.Const &&
        !seen.has(declaration)
      )
        return staticText(declaration.initializer, new Set([...seen, declaration]));
    }
    if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.PlusToken) {
      const left = staticText(node.left, seen);
      const right = staticText(node.right, seen);
      return left === undefined || right === undefined ? undefined : left + right;
    }
    if (ts.isTemplateExpression(node)) {
      let result = node.head.text;
      for (const span of node.templateSpans) {
        const value = staticText(span.expression, seen);
        if (value === undefined) return undefined;
        result += value + span.literal.text;
      }
      return result;
    }
    return undefined;
  }
  function count(text) {
    for (const [value] of text.matchAll(forbidden)) result[value] = (result[value] ?? 0) + 1;
  }
  function visit(node) {
    if (ts.isBinaryExpression(node) || ts.isTemplateExpression(node)) {
      const text = staticText(node);
      if (text !== undefined) {
        count(String(text));
        return;
      }
    }
    if (
      ts.isStringLiteralLike(node) ||
      ts.isTemplateHead(node) ||
      ts.isTemplateMiddle(node) ||
      ts.isTemplateTail(node)
    )
      count(node.text);
    ts.forEachChild(node, visit);
  }
  visit(tree);
  return result;
}
async function files(directory) {
  const result = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) result.push(...(await files(path)));
    else if (path.endsWith(".tsx")) result.push(path);
  }
  return result;
}

test("门禁覆盖直接类名、变量与模板字符串，不扫描注释", () => {
  assert.deepEqual(
    violations(`
    // bg-blue-500 注释不是类名
    const tone = "bg-emerald-400";
    const view = <div className={\`text-[10px] \${tone} \${ok ? "bg-red-100" : "text-amber-500"}\`} />;
    const badge = <span className="shadow-glow bg-grid" />;
  `),
    {
      "emerald-400": 1,
      "text-[10px]": 1,
      "red-100": 1,
      "amber-500": 1,
      "shadow-glow": 1,
      "bg-grid": 1,
    },
  );
});

// 实际数量必须与允许值一致：清理后不收紧会失败，旧额度就不能被后来新增代码复用。
function auditLegacy(current, legacy) {
  const unexpected = [];
  for (const name of new Set([...Object.keys(current), ...Object.keys(legacy)])) {
    const counts = current[name] ?? {};
    const allowed = legacy[name]?.values ?? {};
    for (const value of new Set([...Object.keys(counts), ...Object.keys(allowed)])) {
      if ((counts[value] ?? 0) !== (allowed[value] ?? 0))
        unexpected.push(`${name}: ${value} ${counts[value] ?? 0} != ${allowed[value] ?? 0}`);
    }
  }
  return unexpected;
}

test("静态拼接和模板中的小字号/原生色板不能绕过门禁", () => {
  assert.deepEqual(
    violations(
      'const prefix = "bg-blue-"; const level = "100"; const node = <div className={prefix + level} />;',
    ),
    { "blue-100": 1 },
  );
  assert.deepEqual(violations('const node = <div className={"bg-blue-" + "100"} />;'), {
    "blue-100": 1,
  });
  assert.deepEqual(violations("const node = <div className={`text-[${10}px]`} />;"), {
    "text-[10px]": 1,
  });
});

test("常量折叠遵守词法作用域，内外层同名绑定均不能隐藏违规", () => {
  assert.deepEqual(
    violations(
      'const level = "100"; { const level = "canvas"; } const node = <div className={"bg-blue-" + level} />;',
    ),
    { "blue-100": 1 },
  );
  assert.deepEqual(
    violations(
      'const level = "canvas"; { const level = "100"; const node = <div className={"bg-blue-" + level} />; }',
    ),
    { "blue-100": 1 },
  );
});

test("清理必须同步收紧允许项，减少后的旧额度和已删除文件不能复用", () => {
  const original = { "src/old.tsx": { values: { "text-[10px]": 3 } } };
  const reduced = { "src/old.tsx": { "text-[10px]": 2 } };
  assert.equal(auditLegacy(reduced, original).length, 1);
  assert.equal(auditLegacy({}, original).length, 1);
  const tightened = { "src/old.tsx": { values: { "text-[10px]": 2 } } };
  assert.deepEqual(auditLegacy(reduced, tightened), []);
  assert.equal(auditLegacy({ "src/old.tsx": { "text-[10px]": 3 } }, tightened).length, 1);
  assert.equal(auditLegacy({ "src/new.tsx": { "text-[10px]": 1 } }, tightened).length, 2);
});

test("现存 TSX 违规精确匹配遗留清单，清理时必须同步收紧", async () => {
  const current = {};
  for (const path of await files(sourceRoot)) {
    const name = relative(root, path).replaceAll("\\", "/");
    current[name] = violations(await readFile(path, "utf8"));
  }
  assert.deepEqual(
    auditLegacy(current, legacyViolations),
    [],
    "只能使用 DESIGN.md 语义令牌；减少违规须收紧对应允许项，禁止扩容",
  );
});

const css = await readFile(join(sourceRoot, "styles.css"), "utf8");
const design = await readFile(join(root, "..", "DESIGN.md"), "utf8");
const tokens = Object.fromEntries(
  [...css.matchAll(/--([a-z0-9-]+):\s*(#[a-f0-9]{6});/g)].map((match) => [match[1], match[2]]),
);
function luminance(hex) {
  const channels = hex
    .slice(1)
    .match(/../g)
    .map((value) => parseInt(value, 16) / 255)
    .map((value) => (value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4));
  return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722;
}
function contrast(foreground, background) {
  const a = luminance(tokens[foreground]);
  const b = luminance(tokens[background]);
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}

test("颜色逐项匹配 DESIGN.md，控件圆角保持 6px/8px", () => {
  const colors = design.split("colors:\n")[1].split("\ntypography:")[0];
  for (const [, name, value] of colors.matchAll(/^  ([a-z0-9-]+):\s*"(#[a-f0-9]{6})"/gm))
    assert.equal(tokens[name], value, name);
  assert.match(css, /--radius-sm: 6px;/);
  assert.match(css, /--radius-md: 8px;/);
  assert.doesNotMatch(
    css,
    /--spacing-(?:sm|md|lg|xl|2xl|3xl|4xl|5xl):/,
    "间距别名不能覆盖同名最大宽度容器",
  );
});

test("设计文字与图形组合满足 WCAG 对比度", () => {
  for (const background of ["canvas", "canvas-soft", "surface-hover", "surface-selected"]) {
    for (const text of ["ink", "body", "mute", "status-critical-text"])
      assert.ok(contrast(text, background) >= 4.5, `${text}/${background}`);
  }
  for (const state of ["good", "warning", "serious"])
    assert.ok(contrast(`status-${state}`, `status-${state}-bg`) >= 4.5, state);
  assert.ok(contrast("status-critical-text", "status-critical-bg") >= 4.5);
  assert.ok(contrast("on-primary", "primary") >= 4.5);
  assert.ok(contrast("ink", "destructive-solid") >= 4.5);
  for (const background of ["canvas", "canvas-soft"]) {
    for (const graphic of [
      "border-control",
      "primary-soft",
      "status-critical",
      "chart-series-1",
      "chart-series-2",
    ])
      assert.ok(contrast(graphic, background) >= 3, `${graphic}/${background}`);
  }
});

test("字体只声明本地 latin 文件，主题无需内联脚本", async () => {
  const fonts = await readFile(join(sourceRoot, "fonts.css"), "utf8");
  const html = await readFile(join(root, "index.html"), "utf8");
  assert.equal([...fonts.matchAll(/@font-face/g)].length, 2);
  assert.equal([...fonts.matchAll(/font-display: swap/g)].length, 2);
  assert.deepEqual(
    [...fonts.matchAll(/url\("([^"]+)"\)/g)].map((match) => match[1]),
    [
      "@fontsource-variable/inter/files/inter-latin-wght-normal.woff2",
      "@fontsource-variable/jetbrains-mono/files/jetbrains-mono-latin-wght-normal.woff2",
    ],
  );
  assert.doesNotMatch(html, /fonts\.googleapis|fonts\.gstatic|<script(?![^>]*\bsrc=)[^>]*>/);
  assert.match(html, /class="dark" style="color-scheme: dark"/);
  assert.match(html, /<meta name="color-scheme" content="dark"/);
  assert.doesNotMatch(
    await readFile(join(sourceRoot, "app/root.tsx"), "utf8"),
    /themechange|initTheme|themeMode/,
  );
});
