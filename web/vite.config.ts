import { sveltekit } from '@sveltejs/kit/vite';
import adapter from '@sveltejs/adapter-static';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

// 与 make run / compose 的服务端口契约一致（ZHICHANG_HTTP_ADDR 默认 :8787）；
// 后端跑在别的端口时用 ZHICHANG_BACKEND=http://127.0.0.1:<port> 覆盖
const backend = process.env.ZHICHANG_BACKEND ?? 'http://127.0.0.1:8787';

export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit({
			// #lib 通过 package.json 的 subpath imports 解析
			// 单页应用 + 静态产物，由 Go 侧 embed 后提供服务
			adapter: adapter({ fallback: 'index.html', precompress: false })
		})
		// PWA 不走 vite-plugin-pwa：SvelteKit 3 不支持 transformIndexHtml，
		// 插件既注入不了注册脚本也产不出 sw.js（@vite-pwa/sveltekit 又只声明到 Kit ^2）。
		// 因此 manifest 走 web/static 静态文件，SW 由 scripts/gen-sw.mjs 从
		// src/sw-template.js 注入构建版本后落到 build/sw.js，行为契约照 PWA 规范第四节。
	],
	server: {
		proxy: {
			'/api': { target: backend, changeOrigin: true }
		}
	}
});
