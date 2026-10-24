import js from '@eslint/js';
import prettier from 'eslint-config-prettier';
import react from 'eslint-plugin-react';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import { defineConfig } from 'eslint/config';
import globals from 'globals';
import ts from 'typescript-eslint';

export default defineConfig(
  // ESLint 9 而非 10：eslint-plugin-react@7 的 peer 只到 eslint ^9.7，
  // 在 ESLint 10 上跑 react/display-name 会直接抛
  // "contextOrFilename.getFilename is not a function"（规则 context API 变更）。
  // dist/ 是 vite 的真实输出目录，漏掉它会把压缩产物当源码扫出上千条假错误。
  {
    ignores: ['dist/', 'node_modules/']
  },
  js.configs.recommended,
  ts.configs.recommended,
  react.configs.flat.recommended,
  // 自动 JSX runtime（@vitejs/plugin-react 默认）不引入 React 作用域变量，
  // react-in-jsx-scope 会全场误报。
  react.configs.flat['jsx-runtime'],
  reactHooks.configs.flat['recommended-latest'],
  reactRefresh.configs.recommended,
  prettier,
  {
    languageOptions: {
      globals: { ...globals.browser, ...globals.node },
      parserOptions: { ecmaFeatures: { jsx: true } }
    },
    settings: { react: { version: 'detect' } },
    rules: {
      // 类型由 tsc --noEmit 负责，no-undef 在 TS 项目里会误报全局类型
      'no-undef': 'off',
      // prop 校验交给 TS 类型系统，JS 版规则在 TSX 里只会误报
      'react/prop-types': 'off',
      // Vite HMR 边界提示（文件里同时导出组件与非组件），不是缺陷
      'react-refresh/only-export-components': 'warn',
      // `_` 是「有意丢弃」的约定写法（忽略的回调参数）
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_', caughtErrors: 'none' }
      ]
    }
  }
);
