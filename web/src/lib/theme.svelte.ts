// 主题：明亮 / 暗黑 / 跟随系统。偏好是本机的（localStorage），默认跟随系统。
//
// 与 app.css 的分工：CSS 变量是颜色的唯一事实来源，这里只负责
// 「往 <html> 上挂 data-theme」和「把同一批变量读给 ECharts」——
// 图表拿到的是 canvas 绘制的字面色值，读不到 CSS 变量，所以必须显式取一遍。
export type ThemePreference = 'light' | 'dark' | 'system';
export type ThemeMode = 'light' | 'dark';

const PREF_KEY = 'zhichang.theme';

function readPreference(): ThemePreference {
  try {
    const v = localStorage.getItem(PREF_KEY);
    // 手改过 localStorage 或旧版本写过别的值：只认这三种，其余按默认走
    if (v === 'light' || v === 'dark' || v === 'system') return v;
  } catch {
    /* 隐私模式读不到，按默认跟随系统 */
  }
  return 'system';
}

let preference = $state<ThemePreference>('system');
let systemDark = $state(false);
let mode = $state<ThemeMode>('light');
let started = false;

/** 图表与内联样式要用色时读这里；mode 是 $state，所以在 $effect/$derived 里读它会被追踪，
 *  切主题时图表 option 会跟着重建。 */
export const theme = {
  get preference(): ThemePreference {
    return preference;
  },
  set preference(next: ThemePreference) {
    preference = next;
    try {
      localStorage.setItem(PREF_KEY, next);
    } catch {
      /* 存不下不影响本次浏览 */
    }
    commit();
  },
  get mode(): ThemeMode {
    return mode;
  },
  get systemDark(): boolean {
    return systemDark;
  }
};

function commit() {
  mode = preference === 'system' ? (systemDark ? 'dark' : 'light') : preference;
  const root = document.documentElement;
  root.dataset.theme = mode;
  // 不更新这个，滚动回弹区、原生 select 下拉、表单控件会露出系统底色
  for (const m of document.querySelectorAll('meta[name="color-scheme"]')) {
    m.setAttribute('content', mode);
  }
  // DOM 已同步， palette() 读到的就是新值；顺序不能反
  paletteCache = null;
}

/** 装一次：读偏好、接系统的深浅变化。系统切换要实时生效——「跟随系统」
 *  如果只在刷新时生效，用户在 20:00 打开系统夜间模式时会看到界面停在白天。 */
export function initTheme(): void {
  if (started) return;
  started = true;
  preference = readPreference();
  const mq = window.matchMedia?.('(prefers-color-scheme: dark)');
  systemDark = mq?.matches ?? false;
  mq?.addEventListener('change', (e) => {
    if (preference === 'system') {
      systemDark = e.matches;
      commit();
    }
  });
  commit();
}

/** 图表用的结构色。名字与 app.css 的 token 一一对应，别再抄一份色值。 */
export type ChartPalette = {
  ink: string;
  inkSoft: string;
  inkFaint: string;
  line: string;
  split: string;
  surface: string;
  surface2: string;
  brand: string;
  brandSoft: string;
  warn: string;
  danger: string;
};

// 变量读不到时的兜底（单测或极端环境），取明亮模式的值：
// 读不到变量意味着页面本来就是浅色，兜成深色只会更难看。
const FALLBACK: Record<string, string> = {
  '--color-ink-900': '#16181d',
  '--color-ink-500': '#6b7280',
  '--color-ink-400': '#9aa0a6',
  '--color-line': '#e6e8ec',
  '--color-surface-3': '#f0f1f4',
  '--color-surface': '#ffffff',
  '--color-surface-2': '#fafbfc',
  '--color-brand': '#0f6e56',
  '--color-brand-soft': '#e1f5ee',
  '--color-warn': '#8a6d1f',
  '--color-danger': '#a32d2d'
};

function cssVar(name: string): string {
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return v || FALLBACK[name] || '#000';
}

let paletteCache: ChartPalette | null = null;
let paletteMode: ThemeMode | null = null;

export function palette(): ChartPalette {
  // 先读 mode：调用方的 $effect 靠这一次读取建立依赖，主题切换时 option 才会重建
  const m = mode;
  if (paletteCache && paletteMode === m) return paletteCache;
  paletteCache = {
    ink: cssVar('--color-ink-900'),
    inkSoft: cssVar('--color-ink-500'),
    inkFaint: cssVar('--color-ink-400'),
    line: cssVar('--color-line'),
    split: cssVar('--color-surface-3'),
    surface: cssVar('--color-surface'),
    surface2: cssVar('--color-surface-2'),
    brand: cssVar('--color-brand'),
    brandSoft: cssVar('--color-brand-soft'),
    warn: cssVar('--color-warn'),
    danger: cssVar('--color-danger')
  };
  paletteMode = m;
  return paletteCache;
}

/** 数据色（分期色、指标主色、档位色）在暗底上要提到能看清的亮度。
 *
 *  统一按明度规则换算而不是手抄 30 个「暗色版」：这些色是身份色，
 *  深睡必须还是蓝、清醒必须还是橙，只把亮度抬起来，色相和相互次序都不动。 */
export function dataColor(hex: string): string {
  if (mode !== 'dark') return hex;
  const rgb = parseHex(hex);
  if (!rgb) return hex;
  const [h, s, l] = rgbToHsl(rgb.r, rgb.g, rgb.b);
  const lifted = Math.min(0.82, 0.46 + l * 0.46);
  if (Math.abs(lifted - l) < 0.01) return hex;
  return hslToHex(h, Math.min(1, s * 1.06), lifted);
}

function parseHex(hex: string): { r: number; g: number; b: number } | null {
  const m = /^#?([\da-f]{6})$/i.exec(hex.trim());
  if (!m) return null;
  const n = parseInt(m[1], 16);
  return { r: (n >> 16) & 255, g: (n >> 8) & 255, b: n & 255 };
}

function rgbToHsl(r: number, g: number, b: number): [number, number, number] {
  const rn = r / 255,
    gn = g / 255,
    bn = b / 255;
  const max = Math.max(rn, gn, bn),
    min = Math.min(rn, gn, bn);
  const l = (max + min) / 2;
  if (max === min) return [0, 0, l];
  const d = max - min;
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
  let h: number;
  if (max === rn) h = ((gn - bn) / d + (gn < bn ? 6 : 0)) / 6;
  else if (max === gn) h = ((bn - rn) / d + 2) / 6;
  else h = ((rn - gn) / d + 4) / 6;
  return [h, s, l];
}

function hslToHex(h: number, s: number, l: number): string {
  if (s === 0) {
    const v = Math.round(l * 255);
    return `#${[v, v, v].map((x) => x.toString(16).padStart(2, '0')).join('')}`;
  }
  const q = l < 0.5 ? l * (1 + s) : l + s - l * s;
  const p = 2 * l - q;
  const chan = (t: number) => {
    let x = t;
    if (x < 0) x += 1;
    if (x > 1) x -= 1;
    if (x < 1 / 6) return p + (q - p) * 6 * x;
    if (x < 1 / 2) return q;
    if (x < 2 / 3) return p + (q - p) * (2 / 3 - x) * 6;
    return p;
  };
  return (
    '#' +
    [chan(h + 1 / 3), chan(h), chan(h - 1 / 3)]
      .map((x) =>
        Math.round(x * 255)
          .toString(16)
          .padStart(2, '0')
      )
      .join('')
  );
}
