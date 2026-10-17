import js from '@eslint/js';
import prettier from 'eslint-config-prettier';
import svelte from 'eslint-plugin-svelte';
import { defineConfig } from 'eslint/config';
import globals from 'globals';
import ts from 'typescript-eslint';

export default defineConfig(
  // 构建产物与依赖不参与检查
  { ignores: ['build/', 'node_modules/', '.svelte-kit/', '.svelte-check/'] },
  js.configs.recommended,
  ts.configs.recommended,
  svelte.configs.recommended,
  prettier,
  svelte.configs.prettier,
  {
    languageOptions: { globals: { ...globals.browser, ...globals.node } },
    rules: {
      // 类型由 svelte-check 负责，no-undef 在 TS 项目里会误报全局类型
      'no-undef': 'off',
      // 中文排版里的全角空格（U+3000）是有意使用的分隔符，不是残留的坏字符
      'no-irregular-whitespace': ['error', { skipStrings: true, skipTemplates: true }],
      // 存量放宽：这两类在既有代码里成片存在，一次性改造等于重写前端。
      // 降为 warn 保留可见性，新代码不应再引入。
      '@typescript-eslint/no-explicit-any': 'warn',
      'svelte/prefer-svelte-reactivity': 'warn',
      // `_` 是「有意丢弃」的约定写法（each 的占位变量、忽略的回调参数）
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_', caughtErrors: 'none' }
      ]
    }
  },
  {
    files: ['**/*.svelte', '**/*.svelte.ts', '**/*.svelte.js'],
    languageOptions: {
      parserOptions: {
        extraFileExtensions: ['.svelte'],
        parser: ts.parser
      }
    }
  }
);
