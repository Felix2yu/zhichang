<script lang="ts">
  /**
   * 加载占位。两种形态：文字（默认）与骨架块。
   *
   * `text` 是必填参数，因为文案本身是信息 —— 「波形加载中…」「正在分析心电数据…」
   * 回答的是「系统在做什么」，把它们抹平成「加载中…」会丢掉有效内容。
   * `height` 用于图表区：那个位置必须在加载期间撑住高度，否则页面会抖。
   */
  type Props = {
    text: string;
    /** 骨架块而非文字 */
    skeleton?: boolean;
    /** 占位高度（px）；图表区必给 */
    height?: number;
    class?: string;
  };

  let { text, skeleton = false, height, class: klass = '' }: Props = $props();
</script>

{#if skeleton}
  <div
    class="animate-pulse rounded bg-[var(--color-line)] {klass}"
    style={height ? `height:${height}px` : undefined}
    aria-label={text}
  ></div>
{:else if height}
  <div
    class="flex items-center justify-center text-[12px] text-[var(--color-ink-500)] {klass}"
    style="height:{height}px"
  >
    {text}
  </div>
{:else}
  <span class="text-[12px] text-[var(--color-ink-500)] {klass}">{text}</span>
{/if}
