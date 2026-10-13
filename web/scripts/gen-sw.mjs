// 把 src/sw-template.js 注入构建版本后写成 build/sw.js。
//
// 版本号只用于让缓存名变化从而淘汰旧缓存，因此「内容变了号就得变」：
// 优先用 git describe（tag 或短 SHA），取不到时退回时间戳（本地无 git 场景）。
import { execSync } from 'node:child_process';
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');

function buildVersion() {
	try {
		const rev = execSync('git rev-parse --short HEAD', { cwd: root, stdio: ['ignore', 'pipe', 'ignore'] })
			.toString()
			.trim();
		return rev || Date.now().toString(36);
	} catch {
		return Date.now().toString(36);
	}
}

const src = readFileSync(resolve(root, 'src/sw-template.js'), 'utf8');
// replaceAll：模板的头部注释里也提到这个占位符，replace 只会命中注释里的那一次
const out = src.replaceAll('__BUILD_VERSION__', buildVersion());
if (out === src) {
	console.error('sw-template.js 里的 __BUILD_VERSION__ 没有被替换，检查模板是否被改动');
	process.exit(1);
}

const target = resolve(root, 'build');
mkdirSync(target, { recursive: true });
writeFileSync(resolve(target, 'sw.js'), out);
console.log('已生成 build/sw.js');
