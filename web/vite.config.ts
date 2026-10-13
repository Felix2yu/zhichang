import { sveltekit } from '@sveltejs/kit/vite';
import adapter from '@sveltejs/adapter-static';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

const backend = process.env.ZHICHANG_BACKEND ?? 'http://127.0.0.1:8899';

export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit({
			// #lib 通过 package.json 的 subpath imports 解析
			// 单页应用 + 静态产物，由 Go 侧 embed 后提供服务
			adapter: adapter({ fallback: 'index.html', precompress: false })
		})
	],
	server: {
		proxy: {
			'/api': { target: backend, changeOrigin: true }
		}
	}
});
