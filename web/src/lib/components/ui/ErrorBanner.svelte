<script lang="ts">
  /**
   * 错误横幅。全项目原本 11 处、4 种写法：
   *
   *  - 6 处逐字符相同：`card px-4 py-3 text-[13px]`（sync / sleep / workouts / ecg / trends / settings）
   *  - 概览：同结构但 `py-2.5 text-[12px]`，且渲染的是 `errors[]` 数组而非单个字符串
   *  - sleep 分期错误 / ecg 波形错误 / settings 操作反馈：不带卡片背景（`bare`）
   *  - WorkoutDetail：面板内部的 `border-t` 变体
   *
   * 这里收敛成 `bare` × `size` 两维，任何一处的现有外观都能靠 `class` 原样保留。
   * 顺带把「单条字符串」和「错误数组」两个入口合成一个：空数组 / 空串时不渲染任何东西。
   */
  type Props = {
    /** 单条错误或错误列表；null / undefined / 空数组 / 空串都不渲染 */
    error?: string | string[] | null;
    /** 不带卡片背景，用于表格行内、面板内部等已有容器的位置 */
    bare?: boolean;
    /** md=13px（页面级） / sm=12px（局部） */
    size?: 'md' | 'sm';
    class?: string;
  };

  let { error = null, bare = false, size = 'md', class: klass = '' }: Props = $props();

  let list = $derived(
    (Array.isArray(error) ? error : [error ?? '']).filter((s) => s.trim() !== '')
  );
</script>

{#each list as item (item)}
  <div
    role="alert"
    class="{size === 'md' ? 'text-[13px]' : 'text-[12px]'} text-[var(--color-danger)] {bare
      ? ''
      : size === 'md'
        ? 'card px-4 py-3'
        : 'card px-4 py-2.5'} {klass}"
  >
    {item}
  </div>
{/each}
