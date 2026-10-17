<script lang="ts">
  import { resolve } from '$app/paths';
  import { page } from '$app/state';

  const links = [
    { href: '/', label: '概览' },
    { href: '/trends', label: '趋势' },
    { href: '/sleep', label: '睡眠' },
    { href: '/workouts', label: '运动' },
    { href: '/ecg', label: '心电图' },
    { href: '/sync', label: '数据源' },
    { href: '/settings', label: '设置' }
  ] as const;

  let current = $derived(page.url.pathname);
</script>

<header class="border-b border-[var(--color-line)] bg-[var(--color-surface)]">
  <div class="mx-auto flex max-w-6xl items-center gap-6 px-5 py-3">
    <a href={resolve('/')} class="flex shrink-0 items-baseline gap-2">
      <span class="text-[17px] font-medium tracking-wide">知常</span>
      <span class="hidden text-[12px] text-[var(--color-ink-500)] sm:inline"
        >复命曰常，知常曰明</span
      >
    </a>
    <!-- 七项在窄屏一定会超宽：让它横向滚动而不是换行，导航换行会把正文整块推下去 -->
    <nav class="flex items-center gap-1 overflow-x-auto text-[13px]">
      {#each links as l (l.href)}
        <a
          href={resolve(l.href)}
          class="shrink-0 rounded-md px-2.5 py-1.5 transition-colors {current === l.href
            ? 'bg-[var(--color-brand-soft)] text-[var(--color-brand)]'
            : 'text-[var(--color-ink-700)] hover:bg-[var(--color-line)]/60'}"
        >
          {l.label}
        </a>
      {/each}
    </nav>
  </div>
</header>
