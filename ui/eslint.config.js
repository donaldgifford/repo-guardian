// ESLint for the UI. The load-bearing rule is the markup ban
// (DESIGN-0027 § Security): evidence, PR titles and paths are
// repository-controlled, so nothing may reach the DOM as HTML.
import js from "@eslint/js";
import reactHooks from "eslint-plugin-react-hooks";
import globals from "globals";
import tseslint from "typescript-eslint";

const markupBan = [
  {
    selector: "JSXAttribute[name.name='dangerouslySetInnerHTML']",
    message: "Render text, never markup: repository-controlled values would become HTML (DESIGN-0027).",
  },
  {
    selector: "Property[key.name='dangerouslySetInnerHTML']",
    message: "Render text, never markup: repository-controlled values would become HTML (DESIGN-0027).",
  },
  {
    selector: "AssignmentExpression[left.property.name=/^(innerHTML|outerHTML)$/]",
    message: "Render text, never markup: repository-controlled values would become HTML (DESIGN-0027).",
  },
  {
    selector: "CallExpression[callee.property.name='insertAdjacentHTML']",
    message: "Render text, never markup: repository-controlled values would become HTML (DESIGN-0027).",
  },
];

export default tseslint.config(
  { ignores: ["dist/**", "node_modules/**", "web/src/api/schema.gen.ts"] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    rules: {
      "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_", varsIgnorePattern: "^_", ignoreRestSiblings: true }],
    },
  },
  {
    files: ["web/src/**/*.{ts,tsx}"],
    languageOptions: { globals: globals.browser },
    plugins: { "react-hooks": reactHooks },
    rules: {
      "react-hooks/rules-of-hooks": "error",
      "react-hooks/exhaustive-deps": "warn",
      "no-restricted-syntax": ["error", ...markupBan],
    },
  },
  {
    files: ["server/**/*.ts", "scripts/**/*.ts", "e2e/**/*.ts", "playwright.config.ts"],
    languageOptions: { globals: { ...globals.node, Bun: "readonly" } },
  },
);
