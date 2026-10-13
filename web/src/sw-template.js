/*
 * 知常 Service Worker 模板 —— 构建时由 scripts/gen-sw.mjs 替换 __BUILD_VERSION__ 后落到 build/sw.js。
 *
 * 为什么不是 vite-plugin-pwa：SvelteKit 3 不支持 transformIndexHtml，插件既注入不了
 * 注册脚本也产不出 sw.js；@vite-pwa/sveltekit 的 peer 只到 Kit ^2。
 * 因此这里沿用 Felix2yu/.github/pwa-preset 的手写模板，行为契约照 PWA 规范第四节：
 *   1) 缓存名版本化，activate 按前缀批量删旧；
 *   2) app shell 预缓存（单页应用：'/' 即壳页，任意路由都由它回落）；
 *   3) 导航请求 network-first，失败回落缓存、再回落壳页；
 *   4) /api GET network-first + 缓存回落 + 条目上限（敏感路径不回落壳页）；
 *   5) 静态资源 cache-first；
 *   6) 支持 SKIP_WAITING 即时接管。
 */
const BUILD_VERSION = '__BUILD_VERSION__';
const CACHE_PREFIX = 'zhichang';
const CACHE_VERSION = `${CACHE_PREFIX}-${BUILD_VERSION}`;
const MAX_ENTRIES = 300;

// SPA：'/' 同时充当入口页与离线兜底页（Go 侧对未知路径一律回落 index.html）
const SHELL = ['/'];

self.addEventListener('install', (event) => {
	event.waitUntil(caches.open(CACHE_VERSION).then((cache) => cache.addAll(SHELL)));
	self.skipWaiting();
});

self.addEventListener('activate', (event) => {
	event.waitUntil(
		caches.keys().then((keys) =>
			Promise.all(
				keys
					.filter((k) => k.startsWith(`${CACHE_PREFIX}-`) && k !== CACHE_VERSION)
					.map((k) => caches.delete(k))
			)
		)
	);
	self.clients.claim();
});

self.addEventListener('message', (event) => {
	if (event.data === 'SKIP_WAITING') self.skipWaiting();
});

/** 写缓存并裁剪到上限（FIFO）。 */
async function putWithLimit(request, response) {
	const cache = await caches.open(CACHE_VERSION);
	await cache.put(request, response);
	const keys = await cache.keys();
	if (keys.length > MAX_ENTRIES) {
		for (const k of keys.slice(0, keys.length - MAX_ENTRIES)) {
			await cache.delete(k);
		}
	}
}

self.addEventListener('fetch', (event) => {
	const { request } = event;
	if (request.method !== 'GET') return;

	const url = new URL(request.url);
	if (url.origin !== self.location.origin) return;

	// 导航请求：network-first，离线时回落缓存，再回落壳页
	if (request.mode === 'navigate') {
		event.respondWith(
			fetch(request)
				.then((res) => {
					if (res.ok) putWithLimit(request, res.clone());
					return res;
				})
				// 离线兜底：单页应用的壳页就是 '/'，Go 侧对未知路径一律回落 index.html
				.catch(async () => (await caches.match(request)) || caches.match('/'))
		);
		return;
	}

	// API：network-first + 缓存回落 + 条目上限；失败返回错误而不是壳页，
	// 避免 /api/** 被导航回落吞掉
	if (url.pathname.startsWith('/api/')) {
		event.respondWith(
			fetch(request)
				.then((res) => {
					if (res.ok) putWithLimit(request, res.clone());
					return res;
				})
				.catch(async () => (await caches.match(request)) || Response.error())
		);
		return;
	}

	// 静态资源（含 /_app/immutable 的哈希产物）：cache-first
	event.respondWith(
		caches.match(request).then(
			(cached) =>
				cached ||
				fetch(request).then((res) => {
					if (res.ok) putWithLimit(request, res.clone());
					return res;
				})
		)
	);
});
