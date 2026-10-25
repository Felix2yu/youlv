// 展示层格式化。后端已经把时间算好（本地日期、月份、星期都落库了），
// 前端只负责把秒数说成人话，不再自己算时区。

const pad = (n: number) => String(n).padStart(2, '0');

/** 秒 → 「3 小时 12 分」。不足 1 分给「不到 1 分钟」，避免出现「0 分」。 */
export function dur(sec?: number | null): string {
  if (sec == null || sec < 0) return '—';
  if (sec < 60) return '不到 1 分钟';
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (h === 0) return `${m} 分钟`;
  if (m === 0) return `${h} 小时`;
  return `${h} 小时 ${m} 分`;
}

/** 秒 → 「3:12」，用于紧凑表格与图表轴。 */
export function durShort(sec?: number | null): string {
  if (sec == null || sec < 0) return '—';
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  return h > 0 ? `${h}:${pad(m)}` : `${m}′`;
}

/**
 * 秒 → 面向大数字的紧凑时长：超过 100 小时就不带分钟，
 * 超过 1 万小时换算成「万」，否则在统计卡片里会折成两行。
 */
export function durH(sec?: number | null): string {
  if (sec == null || sec < 0) return '—';
  const h = sec / 3600;
  if (h < 100) return dur(sec);
  if (h < 10000) return `${Math.round(h).toLocaleString('zh-CN')} 小时`;
  return `${(h / 10000).toFixed(2)} 万小时`;
}

/**
 * 库里的本地时间一律是东八区（后端的 arrived_local / local_date 就是这么算的）。
 * 展示层不能再按浏览器时区解释：自托管用户的设备完全可能不在 +08:00，
 * 那样左边时间轴会整片偏移，而且和后端下发的日期字符串对不上。
 *
 * 做法：把时刻平移 +8 小时后用 getUTC* 读取，等价于按 +08:00 格式化，
 * 且不依赖运行环境的时区设置。
 */
const TZ_OFFSET_SEC = 8 * 3600;

function shanghai(ts: number): Date {
  return new Date((ts + TZ_OFFSET_SEC) * 1000);
}

/** Unix 秒 → 「14:05」。 */
export function hhmm(ts?: number | null): string {
  if (!ts) return '—';
  const d = shanghai(ts);
  return `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`;
}

/** Unix 秒 → 「2026-10-23」。 */
export function ymd(ts?: number | null): string {
  if (!ts) return '—';
  const d = shanghai(ts);
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}`;
}

/** Unix 秒 → 「10月23日」。 */
export function md(ts?: number | null): string {
  if (!ts) return '—';
  const d = shanghai(ts);
  return `${d.getUTCMonth() + 1}月${d.getUTCDate()}日`;
}

/** 「2026-10-23」→ 「10月23日 周四」。 */
export function dateCN(date: string): string {
  const [y, m, d] = date.split('-').map(Number);
  if (!y || !m || !d) return date;
  const dow = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'][
    new Date(Date.UTC(y, m - 1, d)).getUTCDay()
  ];
  return `${m}月${d}日 ${dow}`;
}

/** 「2026-10-23」→ 「2026年10月」。 */
export function monthCN(month: string): string {
  const [y, m] = month.split('-').map(Number);
  if (!y || !m) return month;
  return `${y}年${m}月`;
}

/** 「2026-10」→ 上一个月，用于翻月。 */
export function shiftMonth(month: string, delta: number): string {
  const [y, m] = month.split('-').map(Number);
  const d = new Date(Date.UTC(y, m - 1 + delta, 1));
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}`;
}

/** 「2026-10-23」→ 前后一天。 */
export function shiftDate(date: string, delta: number): string {
  const [y, m, d] = date.split('-').map(Number);
  const t = new Date(Date.UTC(y, m - 1, d + delta));
  return `${t.getUTCFullYear()}-${pad(t.getUTCMonth() + 1)}-${pad(t.getUTCDate())}`;
}

export function today(): string {
  return ymd(Math.floor(Date.now() / 1000));
}

/**
 * `<input type="datetime-local">` 的值（「2026-10-26T09:30」）→ Unix 秒。
 *
 * 关键点：输入框里的时间必须按东八区解释，不能用 Date 的本地时区解析——
 * 浏览器在 UTC-5 时 `new Date('2026-10-26T09:30')` 得到的是另一个时刻，
 * 录进去的到访会整体偏移。这里手工按 +08:00 折算。
 */
export function localInputToUnix(v: string): number | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(v);
  if (!m) return null;
  const [, y, mo, d, h, mi] = m.map(Number);
  const ms = Date.UTC(y, mo - 1, d, h, mi) - TZ_OFFSET_SEC * 1000;
  return Math.floor(ms / 1000);
}

/** Unix 秒 → `datetime-local` 的值，同样按东八区。 */
export function unixToLocalInput(ts?: number | null): string {
  if (!ts) return '';
  const d = shanghai(ts);
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}T${pad(
    d.getUTCHours()
  )}:${pad(d.getUTCMinutes())}`;
}

export function num(n: number): string {
  return n.toLocaleString('zh-CN');
}

/** 摄氏度：保留一位并去掉多余的 .0，避免「22.0°」这种占位感。 */
export function temp(c?: number | null): string {
  if (c == null) return '—';
  const r = Math.round(c * 10) / 10;
  return `${Number.isInteger(r) ? r : r.toFixed(1)}°`;
}

export function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
