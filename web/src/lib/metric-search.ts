// 指标搜索：中文子串 + 拼音首字母 + 指标 id 子串。
//
// 51 个指标平铺在界面上时，光靠分类找是找不到的。中文界面里最常见的输入
// 是「tz」而不是「体重」，所以首字母必须能用。
//
// 首字母表手写而不是实时转拼音：只要几十个指标名，写死一张表比引入拼音库
// （几十 KB）划算，也便于校对。表里缺字的指标只是搜不到首字母，不影响中文搜索。
//
// 本模块为纯展示层逻辑，不进入 API / 数据库契约。

/** 指标 id → 拼音首字母（按后端 registry.go 的 display_name_zh 取每字首字母）。 */
const PINYIN: Record<string, string> = {
  step_count: 'bs',
  active_energy: 'hdnl',
  basal_energy_burned: 'jcnl',
  flights_climbed: 'plcs',
  walking_running_distance: 'bxpbjl',
  cycling_distance: 'qxjl',
  dietary_water: 'ysl',
  caffeine: 'kfy',
  mindful_minutes: 'znsc',
  time_in_daylight: 'rzsc',
  exercise_time: 'dlsc',
  stand_time: 'zlsc',
  stand_hour: 'zlxss',
  sleep_analysis: 'smsc',
  toothbrushing: 'sysc',
  sexual_activity: 'shcs',
  heart_rate: 'xl',
  resting_heart_rate: 'jxxl',
  heart_rate_variability: 'xlbyx',
  walking_heart_rate_average: 'bxpjxl',
  cardio_recovery: 'xlhf',
  oxygen_saturation: 'xybhd',
  atrial_fibrillation_burden: 'fcfh',
  respiratory_rate: 'hxpl',
  body_temperature: 'tw',
  sleeping_wrist_temperature: 'smww',
  blood_glucose: 'xt',
  body_mass: 'tz',
  body_mass_index: 'bmi',
  body_fat_percentage: 'tzl',
  lean_body_mass: 'qztz',
  height: 'sg',
  blood_pressure_systolic: 'ssy',
  blood_pressure_diastolic: 'szy',
  vo2_max: 'zdsyl',
  physical_effort: 'tlxh',
  running_power: 'pbgl',
  walking_speed: 'bxsd',
  running_speed: 'pbsd',
  walking_step_length: 'bf',
  walking_asymmetry_percentage: 'bxbdcx',
  walking_double_support_percentage: 'sjzcsjzb',
  six_minute_walking_test_distance: 'lfzbxjl',
  stair_speed_up: 'slsd',
  stair_speed_down: 'xlsd',
  peak_expiratory_flow_rate: 'zdhqls',
  forced_vital_capacity: 'ylfhl',
  environmental_audio_exposure: 'hjyl',
  headphone_audio_exposure: 'ejyl',
  handwashing: 'xscs',
  state_of_mind: 'xlzt'
};

const ASCII = /[\x20-\x7f]/;

/**
 * 取该指标的拼音首字母；表里没有的返回空串（中文搜索与 id 搜索仍可用）。
 *
 * 导出它是为了让测试能断言「字典里的每个指标都有首字母」——
 * 这张表是手写的，新增指标时最容易漏的正是这一步，漏了只能靠人眼发现。
 */
export function pinyinOf(metric: string): string {
  return PINYIN[metric] ?? '';
}

/** 该指标是否命中查询词；空查询命中一切。 */
export function matchMetric(
  def: { metric: string; display_name_zh: string },
  query: string
): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  if (def.display_name_zh.toLowerCase().includes(q)) return true;
  if (def.metric.toLowerCase().includes(q)) return true;
  // 纯 ASCII 输入才走首字母：中文输入进来走上面的子串匹配就够了
  if (ASCII.test(q) && !q.includes(' ')) {
    const py = PINYIN[def.metric];
    if (py && py.startsWith(q)) return true;
  }
  return false;
}
