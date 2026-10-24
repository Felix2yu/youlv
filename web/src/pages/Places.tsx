import { useMemo, useState } from 'react';
import { api } from '../lib/api';
import { useAsync } from '../lib/hooks';
import { md, num } from '../lib/format';
import type { Place, PlaceStats } from '../lib/types';
import { Badge, Card, Empty, ErrorBox, SectionTitle, Spinner } from '../components/ui';

const SORTS = [
  ['visit_count', '到访次数'],
  ['last', '最近到访'],
  ['name', '名称']
] as const;

export default function Places() {
  const [q, setQ] = useState('');
  const [sort, setSort] = useState<string>('visit_count');
  const [onlyCoord, setOnlyCoord] = useState(false);
  const [offset, setOffset] = useState(0);
  const limit = 100;

  const res = useAsync(
    () => api.places({ q, sort, has_coord: onlyCoord, limit, offset }),
    [q, sort, onlyCoord, offset]
  );
  const stats = useAsync(() => api.placesStats(), []);

  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-3">
      {stats.data ? <PlaceOverview s={stats.data} /> : null}
      <Card>
        <div className="flex flex-wrap items-center gap-2">
          <input
            value={q}
            onChange={(e) => {
              setQ(e.target.value);
              setOffset(0);
            }}
            placeholder="搜地点名、城市、地址"
            className="h-8 min-w-0 flex-1 rounded-md border border-line-strong bg-surface px-2 text-[13px] placeholder:text-faint"
          />
          <select
            value={sort}
            onChange={(e) => {
              setSort(e.target.value);
              setOffset(0);
            }}
            className="h-8 rounded-md border border-line-strong bg-surface px-2 text-[12px]"
          >
            {SORTS.map(([v, label]) => (
              <option key={v} value={v}>
                按{label}
              </option>
            ))}
          </select>
          <label className="flex cursor-pointer items-center gap-1.5 text-[12px] text-muted">
            <input
              type="checkbox"
              checked={onlyCoord}
              onChange={(e) => {
                setOnlyCoord(e.target.checked);
                setOffset(0);
              }}
              className="size-3.5 accent-[var(--color-accent)]"
            />
            只看有坐标
          </label>
        </div>
      </Card>

      <Card padded={false}>
        <div className="border-b border-line px-3 py-2">
          <SectionTitle aside={<span className="tnum">{num(res.data?.total ?? 0)} 个</span>}>
            地点库
          </SectionTitle>
        </div>
        {res.loading ? (
          <div className="p-3">
            <Spinner />
          </div>
        ) : res.error ? (
          <div className="p-3">
            <ErrorBox>{res.error}</ErrorBox>
          </div>
        ) : (res.data?.items ?? []).length === 0 ? (
          <Empty hint="导入地点记录后，这里会列出所有去过的地方。">没有匹配的地点</Empty>
        ) : (
          <>
            <ul>
              {(res.data?.items ?? []).map((p) => (
                <PlaceRow key={p.id} p={p} />
              ))}
            </ul>
            <Pager
              offset={offset}
              limit={limit}
              total={res.data?.total ?? 0}
              onChange={setOffset}
            />
          </>
        )}
      </Card>
    </div>
  );
}

function PlaceRow({ p }: { p: Place }) {
  const addr = useMemo(
    () => [p.city, p.district, p.street].filter(Boolean).join(' '),
    [p.city, p.district, p.street]
  );
  return (
    <li className="flex items-center gap-2 border-b border-line px-3 py-2 last:border-0">
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-1.5">
          <span className="truncate text-[13px] font-medium">{p.display_name}</span>
          {p.is_stub ? (
            <Badge tone="warn" title="这个地点只在到访/交通/天气里被提到，没有独立登记过坐标">
              仅引用
            </Badge>
          ) : null}
        </div>
        {addr ? <div className="truncate text-[11px] text-faint">{addr}</div> : null}
      </div>
      <div className="shrink-0 text-right">
        <div className="tnum text-[13px]">{p.visit_count} 次</div>
        {p.last_visit_at ? (
          <div className="tnum text-[11px] text-faint">{md(p.last_visit_at)}</div>
        ) : (
          <div className="text-[11px] text-faint">未到访</div>
        )}
      </div>
      <div className="hidden w-24 shrink-0 text-right text-[11px] text-muted sm:block">
        {p.lat != null ? (
          <span className="tnum">
            {p.lat.toFixed(4)}, {p.lon?.toFixed(4)}
          </span>
        ) : (
          <span className="text-faint">无坐标</span>
        )}
      </div>
    </li>
  );
}

function Pager({
  offset,
  limit,
  total,
  onChange
}: {
  offset: number;
  limit: number;
  total: number;
  onChange: (n: number) => void;
}) {
  const page = Math.floor(offset / limit) + 1;
  const pages = Math.max(1, Math.ceil(total / limit));
  if (pages <= 1) return null;
  return (
    <div className="flex items-center justify-between gap-2 border-t border-line px-3 py-2 text-[12px]">
      <button
        disabled={offset === 0}
        onClick={() => onChange(Math.max(0, offset - limit))}
        className="rounded border border-line-strong px-2 py-0.5 text-muted disabled:opacity-40"
      >
        上一页
      </button>
      <span className="tnum text-faint">
        {page} / {pages}
      </span>
      <button
        disabled={offset + limit >= total}
        onClick={() => onChange(offset + limit)}
        className="rounded border border-line-strong px-2 py-0.5 text-muted disabled:opacity-40"
      >
        下一页
      </button>
    </div>
  );
}

/**
 * 地点库概览。
 *
 * 「仅引用」那格是重点：这些地点只是被别人提到过（到访、交通、天气里出现过），
 * 没有自己的坐标，因此不出现在足迹地图上。补一份地点记录就能升级。
 */
function PlaceOverview({ s }: { s: PlaceStats }) {
  const cells = [
    { label: '全部地点', value: num(s.total) },
    { label: '有坐标', value: num(s.with_coord) },
    { label: '仅引用', value: num(s.stubs), tone: s.stubs > 0 ? 'warn' : undefined },
    { label: '城市', value: num(s.cities) }
  ];
  return (
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
      {cells.map((c) => (
        <div
          key={c.label}
          className="rounded-[var(--radius-card)] border border-line bg-surface px-2.5 py-2"
        >
          <div className="text-[11px] text-faint">{c.label}</div>
          <div
            className={`tnum text-[17px] leading-tight font-semibold ${
              c.tone === 'warn' ? 'text-warn' : 'text-ink'
            }`}
          >
            {c.value}
          </div>
        </div>
      ))}
    </div>
  );
}
