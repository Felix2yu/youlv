import { useMemo } from 'react';
import { api } from '../lib/api';
import { useAsync, useRoute } from '../lib/hooks';
import { durH, num } from '../lib/format';
import type { ActivityStat, CityStat, HourStat, ModeStat, Overview, Tag } from '../lib/types';
import { Bar, Card, Dot, ErrorBox, SectionTitle, Spinner } from '../components/ui';

export default function Stats() {
  const { go } = useRoute();
  const overview = useAsync<Overview>(() => api.overview(), []);
  const acts = useAsync<ActivityStat[]>(() => api.statsActivities(), []);
  const cities = useAsync<CityStat[]>(() => api.statsCities(), []);
  const commute = useAsync<ModeStat[]>(() => api.statsCommute(), []);
  const hourly = useAsync<HourStat[]>(() => api.statsHourly(), []);
  const tags = useAsync(() => api.tags(50), []);

  const loading =
    overview.loading || acts.loading || cities.loading || commute.loading || hourly.loading;
  const err = overview.error ?? acts.error ?? cities.error ?? commute.error ?? hourly.error ?? null;

  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-3">
      {err ? <ErrorBox>{err}</ErrorBox> : null}
      {loading ? (
        <Card>
          <Spinner label="正在汇总" />
        </Card>
      ) : (
        <>
          {overview.data ? <OverviewCards o={overview.data} /> : null}
          {acts.data?.length ? <ActivityPanel items={acts.data} /> : null}
          {cities.data?.length ? <CityPanel items={cities.data} /> : null}
          {commute.data?.length ? <CommutePanel items={commute.data} /> : null}
          {tags.data?.length ? (
            <TagPanel items={tags.data} onPick={(name) => go('/timeline', { q: name })} />
          ) : null}
          {hourly.data?.length ? <HourPanel items={hourly.data} /> : null}
        </>
      )}
    </div>
  );
}

/** 标签分布。点一个标签跳到时间轴搜索该标签的到访。 */
function TagPanel({ items, onPick }: { items: Tag[]; onPick: (name: string) => void }) {
  const max = Math.max(1, ...items.map((i) => i.count));
  return (
    <Card>
      <SectionTitle aside={<span className="tnum">{items.length} 个标签</span>}>标签</SectionTitle>
      <ul className="flex flex-col gap-1">
        {items.map((t) => (
          <li key={t.id}>
            <button
              onClick={() => onPick(t.name)}
              className="flex w-full items-center gap-2 rounded px-1 py-0.5 text-left transition-colors hover:bg-raised"
              title={`搜索「${t.name}」的到访`}
            >
              <Dot color={t.color} />
              <span className="w-16 shrink-0 truncate text-[12px]">{t.name}</span>
              <div className="min-w-0 flex-1">
                <Bar value={t.count} max={max} color={t.color} />
              </div>
              <span className="tnum w-14 shrink-0 text-right text-[12px] text-muted">
                {t.count} 次
              </span>
            </button>
          </li>
        ))}
      </ul>
    </Card>
  );
}

function OverviewCards({ o }: { o: Overview }) {
  const cells = [
    { label: '到访', value: num(o.visits), unit: '次' },
    { label: '地点', value: num(o.places), unit: '个' },
    { label: '有记录的天', value: num(o.days), unit: '天' },
    { label: '累计停留', value: durH(o.total_duration_s), unit: '' },
    { label: '城市', value: num(o.cities), unit: '个' },
    { label: '交通', value: num(o.trips), unit: '段' }
  ];
  return (
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6">
      {cells.map((c) => (
        <div
          key={c.label}
          className="rounded-[var(--radius-card)] border border-line bg-surface px-2.5 py-2"
        >
          <div className="text-[11px] text-faint">{c.label}</div>
          <div className="tnum text-[17px] leading-tight font-semibold">
            {c.value}
            {c.unit ? (
              <span className="ml-0.5 text-[11px] font-normal text-muted">{c.unit}</span>
            ) : null}
          </div>
        </div>
      ))}
    </div>
  );
}

function ActivityPanel({ items }: { items: ActivityStat[] }) {
  const max = Math.max(1, ...items.map((i) => i.duration_s));
  const total = items.reduce((a, b) => a + b.duration_s, 0);
  return (
    <Card>
      <SectionTitle aside={<span className="tnum">共 {durH(total)}</span>}>
        时间都花在哪
      </SectionTitle>
      <ul className="flex flex-col gap-1.5">
        {items.map((i) => (
          <li key={i.name} className="flex items-center gap-2">
            <Dot color={i.color} />
            <span className="w-16 shrink-0 truncate text-[12px]">{i.name}</span>
            <div className="min-w-0 flex-1">
              <Bar value={i.duration_s} max={max} color={i.color} />
            </div>
            <span className="tnum w-20 shrink-0 text-right text-[12px] text-muted">
              {durH(i.duration_s)}
            </span>
            <span className="tnum w-12 shrink-0 text-right text-[11px] text-faint">
              {i.count} 次
            </span>
          </li>
        ))}
      </ul>
    </Card>
  );
}

function CityPanel({ items }: { items: CityStat[] }) {
  const max = Math.max(1, ...items.map((i) => i.duration_s));
  return (
    <Card>
      <SectionTitle aside={<span className="tnum">{items.length} 座</span>}>城市护照</SectionTitle>
      <ul className="flex flex-col gap-1.5">
        {items.slice(0, 30).map((c) => (
          <li key={`${c.province}-${c.city}`} className="flex items-center gap-2">
            <span className="w-20 shrink-0 truncate text-[12px]">{c.city}</span>
            <div className="min-w-0 flex-1">
              <Bar value={c.duration_s} max={max} />
            </div>
            <span className="tnum w-20 shrink-0 text-right text-[12px] text-muted">
              {durH(c.duration_s)}
            </span>
            <span className="tnum w-14 shrink-0 text-right text-[11px] leading-tight text-faint">
              {c.places} 地
              <br />
              {c.visits} 次
            </span>
          </li>
        ))}
      </ul>
      {items.length > 30 ? (
        <div className="mt-2 text-[11px] text-faint">只显示停留最久的 30 座城市</div>
      ) : null}
    </Card>
  );
}

function CommutePanel({ items }: { items: ModeStat[] }) {
  const max = Math.max(1, ...items.map((i) => i.duration_s));
  return (
    <Card>
      <SectionTitle aside={<span className="tnum">{items.length} 种</span>}>怎么去的</SectionTitle>
      <ul className="flex flex-col gap-1.5">
        {items.map((m) => (
          <li key={m.mode} className="flex items-center gap-2">
            <span className="w-16 shrink-0 truncate text-[12px]">{m.mode}</span>
            <div className="min-w-0 flex-1">
              <Bar value={m.duration_s} max={max} />
            </div>
            <span className="tnum w-20 shrink-0 text-right text-[12px] text-muted">
              {durH(m.duration_s)}
            </span>
            <span className="tnum w-12 shrink-0 text-right text-[11px] text-faint">
              {m.count} 段
            </span>
          </li>
        ))}
      </ul>
    </Card>
  );
}

/** 时段热力：横轴 24 小时，纵轴周一到周日（后端 dow：0=周日）。 */
function HourPanel({ items }: { items: HourStat[] }) {
  const grid = useMemo(() => {
    const m = new Map<string, number>();
    for (const h of items) m.set(`${h.dow}-${h.hour}`, h.n);
    return m;
  }, [items]);
  const max = useMemo(() => Math.max(1, ...items.map((i) => i.n)), [items]);
  // 把周日挪到最后，符合中文日历的习惯排法
  const rows = [1, 2, 3, 4, 5, 6, 0];
  const names = ['一', '二', '三', '四', '五', '六', '日'];

  return (
    <Card>
      <SectionTitle aside={<span className="tnum">峰值 {max} 次</span>}>
        什么时候在外面
      </SectionTitle>
      <div className="overflow-x-auto">
        <div className="min-w-[520px]">
          <div className="mb-0.5 flex gap-px pl-6">
            {Array.from({ length: 24 }, (_, h) => (
              <div key={h} className="flex-1 text-center text-[9px] text-faint">
                {h % 3 === 0 ? h : ''}
              </div>
            ))}
          </div>
          {rows.map((dow, ri) => (
            <div key={dow} className="flex items-center gap-px">
              <div className="w-6 shrink-0 text-[10px] text-faint">{names[ri]}</div>
              {Array.from({ length: 24 }, (_, h) => {
                const n = grid.get(`${dow}-${h}`) ?? 0;
                const a = n === 0 ? 0 : 0.12 + (n / max) * 0.88;
                return (
                  <div
                    key={h}
                    title={`周${names[ri]} ${h}:00 — ${n} 次`}
                    className="h-4 flex-1 rounded-[2px]"
                    style={{
                      backgroundColor:
                        n === 0
                          ? 'var(--color-raised)'
                          : `color-mix(in srgb, var(--color-accent) ${(a * 100).toFixed(0)}%, transparent)`
                    }}
                  />
                );
              })}
            </div>
          ))}
        </div>
      </div>
    </Card>
  );
}
