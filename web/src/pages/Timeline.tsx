import { useMemo, useState } from 'react';
import { api } from '../lib/api';
import { useAsync, useRoute } from '../lib/hooks';
import { dateCN, dur, hhmm, monthCN, shiftDate, shiftMonth, today } from '../lib/format';
import { Badge, Button, Card, Dot, Empty, ErrorBox, SectionTitle, Spinner } from '../components/ui';

type Mode = 'day' | 'month';

export default function Timeline() {
  const { query, go } = useRoute();
  const [mode, setMode] = useState<Mode>('day');
  const [date, setDate] = useState(() => query.get('date') || today());
  const month = useMemo(() => date.slice(0, 7), [date]);

  const day = useAsync(() => api.timelineDay(date), [date]);
  const buckets = useAsync(() => api.timelineMonth(month), [month]);

  const jump = (d: string) => {
    setDate(d);
    go('/timeline', { date: d });
  };

  const maxCount = useMemo(
    () => Math.max(1, ...(buckets.data ?? []).map((b) => b.count)),
    [buckets.data]
  );

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" variant="plain" onClick={() => jump(shiftDate(date, -1))} title="前一天">
          ←
        </Button>
        <input
          type="date"
          value={date}
          onChange={(e) => e.target.value && jump(e.target.value)}
          className="h-7 rounded-md border border-line-strong bg-surface px-2 text-[13px] text-ink"
        />
        <Button
          size="sm"
          variant="plain"
          onClick={() => jump(shiftDate(date, 1))}
          title="后一天"
          disabled={date >= today()}
        >
          →
        </Button>
        <Button size="sm" variant={mode === 'day' ? 'primary' : 'ghost'} onClick={() => setMode('day')}>
          当天
        </Button>
        <Button
          size="sm"
          variant={mode === 'month' ? 'primary' : 'ghost'}
          onClick={() => setMode('month')}
        >
          整月
        </Button>
        <span className="ml-auto text-[12px] text-faint">{dateCN(date)}</span>
      </div>

      {mode === 'month' ? (
        <Card>
          <SectionTitle
            aside={
              <span className="tnum">
                {(buckets.data ?? []).reduce((a, b) => a + b.count, 0)} 次到访
              </span>
            }
          >
            {monthCN(month)}
          </SectionTitle>
          {buckets.loading ? (
            <Spinner />
          ) : buckets.error ? (
            <ErrorBox>{buckets.error}</ErrorBox>
          ) : (
            <MonthGrid
              month={month}
              counts={Object.fromEntries((buckets.data ?? []).map((b) => [b.date, b.count]))}
              max={maxCount}
              active={date}
              onPick={jump}
            />
          )}
        </Card>
      ) : null}

      <Card padded={false}>
        <div className="border-b border-line px-3 py-2">
          <SectionTitle aside={<span className="tnum">{(day.data ?? []).length} 条</span>}>
            {dateCN(date)}
          </SectionTitle>
        </div>
        {day.loading ? (
          <div className="p-3">
            <Spinner />
          </div>
        ) : day.error ? (
          <div className="p-3">
            <ErrorBox>{day.error}</ErrorBox>
          </div>
        ) : (day.data ?? []).length === 0 ? (
          <Empty hint="换个日期试试，或者先导入 Rond 导出的数据。">这一天还没有到访记录</Empty>
        ) : (
          <ul>
            {(day.data ?? []).map((v) => (
              <li key={v.id} className="flex gap-3 border-b border-line px-3 py-2 last:border-0">
                <div className="w-10 shrink-0 pt-0.5 text-right">
                  <div className="tnum text-[13px] font-medium">{hhmm(v.arrived_at)}</div>
                  <div className="tnum text-[11px] text-faint">
                    {v.departed_at ? hhmm(v.departed_at) : '—'}
                  </div>
                </div>
                <div className="flex min-w-0 flex-1 flex-col gap-1">
                  <div className="flex min-w-0 items-center gap-1.5">
                    <Dot color={v.color} />
                    <span className="truncate text-[13px] font-medium">{v.place_name}</span>
                    {v.city ? <span className="text-[11px] text-faint">{v.city}</span> : null}
                    {v.weather_level && v.weather_level !== 'exact' ? (
                      <Badge tone="warn" title="这一刻没有精确天气，用了附近时间或地点的近似值">
                        天气近似
                      </Badge>
                    ) : null}
                  </div>
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[11px] text-muted">
                    <span>{v.activity || '未分类'}</span>
                    <span className="tnum">{dur(v.duration_s)}</span>
                    {v.temp_c != null ? <span className="tnum">{Math.round(v.temp_c)}°</span> : null}
                    {v.condition ? <span>{v.condition}</span> : null}
                    {v.tags?.map((t) => (
                      <Badge key={t}>{t}</Badge>
                    ))}
                  </div>
                  {v.note ? (
                    <div className="text-[12px] leading-relaxed text-muted">{v.note}</div>
                  ) : null}
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  );
}

/** 月历热力图。格子颜色深浅表示当天到访次数。 */
function MonthGrid({
  month,
  counts,
  max,
  active,
  onPick
}: {
  month: string;
  counts: Record<string, number>;
  max: number;
  active: string;
  onPick: (d: string) => void;
}) {
  const [y, m] = month.split('-').map(Number);
  const first = new Date(y, m - 1, 1);
  const daysInMonth = new Date(y, m, 0).getDate();
  const lead = first.getDay();

  const cells: (string | null)[] = [
    ...Array.from({ length: lead }, () => null),
    ...Array.from({ length: daysInMonth }, (_, i) => {
      const d = `${month}-${String(i + 1).padStart(2, '0')}`;
      return d;
    })
  ];

  return (
    <div>
      <div className="mb-1 grid grid-cols-7 gap-1 text-center text-[11px] text-faint">
        {['日', '一', '二', '三', '四', '五', '六'].map((d) => (
          <div key={d}>{d}</div>
        ))}
      </div>
      <div className="grid grid-cols-7 gap-1">
        {cells.map((d, i) => {
          if (!d) return <div key={`e${i}`} />;
          const n = counts[d] ?? 0;
          const alpha = n === 0 ? 0 : 0.2 + (n / max) * 0.8;
          return (
            <button
              key={d}
              onClick={() => onPick(d)}
              title={`${d}：${n} 次到访`}
              className={`grid aspect-square place-items-center rounded text-[11px] transition-colors tnum ${
                d === active ? 'ring-1 ring-accent' : ''
              } ${n === 0 ? 'text-faint hover:bg-raised' : 'text-white'}`}
              style={n > 0 ? { backgroundColor: `color-mix(in srgb, var(--color-accent) ${alpha * 100}%, transparent)` } : undefined}
            >
              {Number(d.slice(-2))}
            </button>
          );
        })}
      </div>
      <div className="mt-2 flex items-center justify-between text-[11px] text-faint">
        <Button size="sm" variant="plain" onClick={() => onPick(`${shiftMonth(month, -1)}-01`)}>
          {monthCN(shiftMonth(month, -1))}
        </Button>
        <Button size="sm" variant="plain" onClick={() => onPick(`${shiftMonth(month, 1)}-01`)}>
          {monthCN(shiftMonth(month, 1))}
        </Button>
      </div>
    </div>
  );
}
