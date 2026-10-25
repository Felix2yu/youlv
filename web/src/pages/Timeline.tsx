import { useMemo, useState } from 'react';
import { api } from '../lib/api';
import { useAsync, useRoute } from '../lib/hooks';
import { dateCN, dur, hhmm, monthCN, shiftDate, shiftMonth, today } from '../lib/format';
import { Badge, Button, Card, Dot, Empty, ErrorBox, SectionTitle, Spinner } from '../components/ui';
import VisitDrawer from '../components/VisitDrawer';
import ManualEntry from '../components/ManualEntry';
import { MODE_CN, type Trip, type Visit } from '../lib/types';

type Mode = 'day' | 'month' | 'search';

export default function Timeline({ onDataChanged }: { onDataChanged?: () => void }) {
  const { query, go } = useRoute();
  const [mode, setMode] = useState<Mode>(query.get('q') ? 'search' : 'day');
  const [date, setDate] = useState(() => query.get('date') || today());
  const [manualOpen, setManualOpen] = useState(false);
  const month = useMemo(() => date.slice(0, 7), [date]);

  // 搜索条件分「输入中」与「已提交」两份：输入框每敲一个字都发一次请求
  // 会在慢网下打出成吨无用流量，提交后才查。
  const [q, setQ] = useState(() => query.get('q') || '');
  const [activity, setActivity] = useState(() => query.get('activity') || '');
  const [from, setFrom] = useState(() => query.get('from') || '');
  const [to, setTo] = useState(() => query.get('to') || '');
  const [applied, setApplied] = useState(() => ({
    q: query.get('q') || '',
    activity: query.get('activity') || '',
    from: query.get('from') || '',
    to: query.get('to') || ''
  }));

  const [visitId, setVisitId] = useState<number | null>(() => {
    const v = query.get('visit');
    return v && Number(v) > 0 ? Number(v) : null;
  });

  const day = useAsync(() => api.timelineDay(date), [date]);
  // 交通段单独取：时间轴的「这段路怎么走的」全在 trips 里，
  // 之前后端根本没有读端点，界面也就只能显示一串孤立的停留。
  const dayTrips = useAsync(() => api.trips({ from: date, to: date }), [date]);
  const buckets = useAsync(() => api.timelineMonth(month), [month]);
  const activities = useAsync(() => api.activities(), []);
  const results = useAsync(
    () =>
      api.timelineRange({
        q: applied.q,
        activity: applied.activity,
        from: applied.from,
        to: applied.to,
        limit: 200
      }),
    [applied.q, applied.activity, applied.from, applied.to]
  );

  const jump = (d: string) => {
    setDate(d);
    go('/timeline', { date: d });
  };

  const maxCount = useMemo(
    () => Math.max(1, ...(buckets.data ?? []).map((b) => b.count)),
    [buckets.data]
  );

  const openVisit = (id: number) => {
    setVisitId(id);
    go('/timeline', { date, visit: id });
  };

  const closeVisit = () => {
    setVisitId(null);
    go('/timeline', { date });
  };

  // 手动录入或删改之后，当天列表、当月热力图、侧栏计数都会变。
  const refresh = () => {
    day.reload();
    dayTrips.reload();
    buckets.reload();
    results.reload();
    onDataChanged?.();
  };

  const submitSearch = () => {
    const next = { q, activity, from, to };
    setApplied(next);
    setMode('search');
    go('/timeline', { q: next.q, activity: next.activity, from: next.from, to: next.to });
  };

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
        <Button
          size="sm"
          variant={mode === 'day' ? 'primary' : 'ghost'}
          onClick={() => {
            setMode('day');
            go('/timeline', { date });
          }}
        >
          当天
        </Button>
        <Button
          size="sm"
          variant={mode === 'month' ? 'primary' : 'ghost'}
          onClick={() => {
            setMode('month');
            go('/timeline', { date });
          }}
        >
          整月
        </Button>
        <Button
          size="sm"
          variant={mode === 'search' ? 'primary' : 'ghost'}
          onClick={() => setMode('search')}
        >
          搜索
        </Button>
        <span className="ml-auto hidden text-[12px] text-faint sm:inline">{dateCN(date)}</span>
        <a
          href={api.exportVisitsUrl(
            mode === 'search'
              ? { from: applied.from || undefined, to: applied.to || undefined }
              : { from: date, to: date }
          )}
          download
          className="inline-flex h-7 items-center rounded-md border border-line-strong bg-surface px-2 text-[12px] font-medium text-ink transition-colors hover:bg-raised"
          title="把当前范围的到访导出成 CSV（含两套坐标）"
        >
          导出
        </a>
        <Button size="sm" variant="primary" onClick={() => setManualOpen(true)}>
          记一笔
        </Button>
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

      {mode === 'search' ? (
        <Card>
          <SectionTitle>搜索到访</SectionTitle>
          <div className="flex flex-wrap items-center gap-2">
            <input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && submitSearch()}
              placeholder="地点、备注或标签"
              className="h-8 min-w-0 flex-1 rounded-md border border-line-strong bg-surface px-2 text-[13px] placeholder:text-faint"
            />
            <select
              value={activity}
              onChange={(e) => setActivity(e.target.value)}
              className="h-8 rounded-md border border-line-strong bg-surface px-2 text-[12px]"
            >
              <option value="">全部活动</option>
              {(activities.data ?? []).map((a) => (
                <option key={a.name} value={a.name}>
                  {a.name}
                </option>
              ))}
            </select>
            <input
              type="date"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
              className="h-8 rounded-md border border-line-strong bg-surface px-2 text-[12px]"
            />
            <span className="text-[12px] text-faint">至</span>
            <input
              type="date"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              className="h-8 rounded-md border border-line-strong bg-surface px-2 text-[12px]"
            />
            <Button variant="primary" onClick={submitSearch}>
              搜索
            </Button>
          </div>
        </Card>
      ) : null}

      {mode === 'search' ? (
        <Card padded={false}>
          <div className="border-b border-line px-3 py-2">
            <SectionTitle
              aside={<span className="tnum">{(results.data?.items ?? []).length} 条</span>}
            >
              搜索结果
            </SectionTitle>
          </div>
          {results.loading ? (
            <div className="p-3">
              <Spinner />
            </div>
          ) : results.error ? (
            <div className="p-3">
              <ErrorBox>{results.error}</ErrorBox>
            </div>
          ) : (results.data?.items ?? []).length === 0 ? (
            <Empty hint="换个关键词，或清掉筛选条件。">没有匹配的到访</Empty>
          ) : (
            <ul>
              {(results.data?.items ?? []).map((v) => (
                <VisitRow key={v.id} v={v} onOpen={openVisit} showDate />
              ))}
            </ul>
          )}
        </Card>
      ) : (
        <Card padded={false}>
          <div className="border-b border-line px-3 py-2">
            <SectionTitle
              aside={
                <span className="tnum">
                  {(day.data ?? []).length} 条
                  {(dayTrips.data?.items ?? []).length > 0
                    ? ` · ${(dayTrips.data?.items ?? []).length} 段路程`
                    : ''}
                </span>
              }
            >
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
          ) : (day.data ?? []).length === 0 && (dayTrips.data?.items ?? []).length === 0 ? (
            <Empty hint="换个日期试试，或者先导入 Rond 导出的数据；临时想补一条可以直接点右上角「记一笔」。">
              这一天还没有记录
            </Empty>
          ) : (
            <DayTimeline
              visits={day.data ?? []}
              trips={dayTrips.data?.items ?? []}
              onOpen={openVisit}
            />
          )}
        </Card>
      )}

      <VisitDrawer id={visitId} onClose={closeVisit} onChanged={refresh} />

      {manualOpen ? (
        <ManualEntry
          activities={activities.data ?? []}
          defaultDate={date}
          onClose={() => setManualOpen(false)}
          onDone={() => {
            setManualOpen(false);
            refresh();
          }}
        />
      ) : null}
    </div>
  );
}

/** 一天的时间轴：停留与路程按时间穿插，而不是两串各自排。 */
function DayTimeline({
  visits,
  trips,
  onOpen
}: {
  visits: Visit[];
  trips: Trip[];
  onOpen: (id: number) => void;
}) {
  const items = useMemo(() => {
    const all: ({ at: number; kind: 'visit'; v: Visit } | { at: number; kind: 'trip'; t: Trip })[] =
      [
        ...visits.map((v) => ({ at: v.arrived_at, kind: 'visit' as const, v })),
        ...trips.map((t) => ({ at: t.from_at, kind: 'trip' as const, t }))
      ];
    return all.sort((a, b) => a.at - b.at);
  }, [visits, trips]);

  return (
    <ul>
      {items.map((it) =>
        it.kind === 'visit' ? (
          <VisitRow key={`v${it.v.id}`} v={it.v} onOpen={onOpen} />
        ) : (
          <TripRow key={`t${it.t.id}`} t={it.t} />
        )
      )}
    </ul>
  );
}

/** 一段路程。刻意做得比到访行浅一档：它是背景信息，不是主体。 */
function TripRow({ t }: { t: Trip }) {
  const mode = MODE_CN[t.mode] || t.mode_raw || '出行';
  return (
    <li className="border-b border-line bg-raised/40 last:border-0">
      <div className="flex gap-3 px-3 py-1.5">
        <div className="w-10 shrink-0 text-right">
          <div className="tnum text-[11px] text-faint">{hhmm(t.from_at)}</div>
          <div className="tnum text-[11px] text-faint">{hhmm(t.to_at)}</div>
        </div>
        <div className="flex min-w-0 flex-1 items-center gap-1.5 text-[12px] text-muted">
          <span className="shrink-0 text-faint" aria-hidden>
            ↓
          </span>
          <span className="shrink-0">{mode}</span>
          <span className="truncate">
            {t.from_place || '未知地点'} → {t.to_place || '未知地点'}
          </span>
          <span className="tnum ml-auto shrink-0 text-[11px] text-faint">{dur(t.duration_s)}</span>
        </div>
      </div>
    </li>
  );
}

/** 到访行。整行可点开详情。 */
function VisitRow({
  v,
  onOpen,
  showDate
}: {
  v: Visit;
  onOpen: (id: number) => void;
  showDate?: boolean;
}) {
  return (
    <li className="border-b border-line last:border-0">
      <button
        onClick={() => onOpen(v.id)}
        className="flex w-full gap-3 px-3 py-2 text-left transition-colors hover:bg-raised"
      >
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
            {showDate ? (
              <span className="tnum shrink-0 text-[11px] text-faint">{v.local_date}</span>
            ) : null}
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
          {v.note ? <div className="text-[12px] leading-relaxed text-muted">{v.note}</div> : null}
        </div>
      </button>
    </li>
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
              className={`tnum grid aspect-square place-items-center rounded text-[11px] transition-colors ${
                d === active ? 'ring-1 ring-accent' : ''
              } ${n === 0 ? 'text-faint hover:bg-raised' : 'text-white'}`}
              style={
                n > 0
                  ? {
                      backgroundColor: `color-mix(in srgb, var(--color-accent) ${alpha * 100}%, transparent)`
                    }
                  : undefined
              }
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
