import js from "@eslint/js";
import eslintPluginPrettier from "eslint-plugin-prettier/recommended";
import globals from "globals";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import tseslint from "typescript-eslint";

export default tseslint.config(
  // 构建输出和框架缓存不参与检查，源码目录仍必须零警告通过。
  { ignores: ["dist", ".output", ".vinxi"] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    plugins: {
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      // 浏览器 SPA 不允许引入服务端专用标记；误用会把服务端假设带入客户端构建。
      "no-restricted-imports": [
        "error",
        {
          paths: [
            // 通知必须通过 notify，确保成功/信息提示遵循用户偏好；禁止别名及命名空间绕过。
            { name: "sonner", message: "请从 @/lib/notify 导入 notify，统一通知偏好与错误提示。" },
            {
              name: "server-only",
              message: "本项目为浏览器 SPA，不能导入 server-only；服务端逻辑应放在 Go 后端。",
            },
          ],
        },
      ],
      "react-refresh/only-export-components": ["error", { allowConstantExport: true }],
      "@typescript-eslint/no-unused-vars": "off",
    },
  },
  // 仅通知适配层与展示容器可直接接触 sonner，其他规则（如 server-only）继续有效。
  {
    files: ["src/lib/notify.ts", "src/components/ui/sonner.tsx"],
    rules: {
      "no-restricted-imports": [
        "error",
        { paths: [{ name: "server-only", message: "浏览器 SPA 不能导入 server-only。" }] },
      ],
    },
  },
  // shadcn/ui 基础组件会同时导出样式变体与辅助函数，因此对该目录关闭单组件导出限制。
  {
    files: ["src/components/ui/**/*.{ts,tsx}"],
    rules: {
      "react-refresh/only-export-components": "off",
    },
  },
  // UI 重构已消除历史 any，全部业务目录统一使用严格的 no-explicit-any 规则。
  eslintPluginPrettier,
);
