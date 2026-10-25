import { api } from '../lib/api';
import { useAsync } from '../lib/hooks';
import { dur, md, num } from '../lib/format';
import type { PlaceDetail } from '../lib/types';
import { Badge, Dot, ErrorBox, Row, Spinner } from './ui';

/**
 * 地点详情抽屉。
 *
 * 与到访抽屉同一套交互：右侧滑出、遮罩可点、Esc 关闭由调用方统一处理。
 * 内容里最要紧的是「这个地点我到过几次、什么时候来的」——idx_visits_place_time
 * 索引就是为这条查询建的，此前前端一直没有入口。
 */
export default function PlaceDrawer({
  id,
  onClose,
  onOpenVisit
}: {
  id: number | null;
  onClose: () => void;
  onOpenVisit?: (visitId: number, date: string) => void;
}) {
  const detail = useAsync<PlaceDetail | null>(
    () => (id ? api.place(id) : Promise.resolve(null)),
    [id]
  );

  if (id === null) return null;

  return (
    <div className="fixed inset-0 z-20 flex justify-end" role="dialog" aria-modal="true">
      <button className="absolute inset-0 bg-black/25" onClick={onClose} aria-label="关闭详情" />
      <aside className="relative flex h-full w-full max-w-sm flex-col overflow-y-auto border-l border-line bg-surface shadow-xl">
        <header className="sticky top-0 flex items-center gap-2 border-b border-line bg-surface px-3 py-2">
          <span className="text-[13px] font-semibold">地点详情</span>
          <button
            onClick={onClose}
            className="ml-auto rounded px-1.5 py-0.5 text-[13px] text-muted hover:bg-raised hover:text-ink"
          >
            关闭
          </button>
        </header>

        <div className="flex-1 px-3 py-3">
          {detail.loading ? (
            <Spinner label="正在读取" />
          ) : detail.error ? (
            <ErrorBox>{detail.error}</ErrorBox>
          ) : !detail.data ? (
            <div className="py-8 text-center text-[13px] text-muted">没有这个地点</div>
          ) : (
            <Detail d={detail.data} onOpenVisit={onOpenVisit} />
          )}
        </div>
      </aside>
    </div>
  );
}

function Detail({
  d,
  onOpenVisit
}: {
  d: PlaceDetail;
  onOpenVisit?: (visitId: number, date: string) => void;
}) {
  const addr = [d.city, d.admin_area, d.district, d.street].filter(Boolean).join(' ');
  return (
    <div className="flex flex-col gap-3">
      <div>
        <div className="text-[15px] font-semibold">{d.display_name || d.name}</div>
        <div className="mt-0.5 text-[12px] text-muted">{addr || '未标注地址'}</div>
        <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
          {d.is_stub ? (
            <Badge tone="warn" title="这个地点只在到访/交通/天气里被提到，没有独立登记过坐标">
              仅引用
            </Badge>
          ) : (
            <Badge tone="accent">已登记</Badge>
          )}
          <Badge>{d.origin === 'manual' ? '手动录入' : d.origin}</Badge>
        </div>
      </div>

      <div>
        <Row k="到访次数" v={num(d.visit_count)} />
        <Row k="累计停留" v={dur(d.duration_s)} />
        <Row k="首次到访" v={d.first_visit_at ? md(d.first_visit_at) : '—'} />
        <Row k="最近到访" v={d.last_visit_at ? md(d.last_visit_at) : '—'} />
      </div>

      <div>
        <div className="mb-1 text-[11px] text-faint">坐标</div>
        <div className="tnum text-[12px] text-muted">
          {d.lat != null ? (
            <>
              WGS-84 {d.lat.toFixed(6)}, {d.lon?.toFixed(6)}
              <br />
              GCJ-02 {d.lat_gcj?.toFixed(6)}, {d.lon_gcj?.toFixed(6)}
            </>
          ) : (
            '这个地点没有坐标，不会出现在足迹地图上'
          )}
        </div>
      </div>

      <div>
        <div className="mb-1 text-[11px] text-faint">最近到访</div>
        {d.recent.length === 0 ? (
          <div className="py-3 text-[12px] text-faint">还没有到访记录</div>
        ) : (
          <ul className="divide-y divide-line">
            {d.recent.map((v) => (
              <li key={v.id}>
                <button
                  onClick={() => onOpenVisit?.(v.id, v.local_date)}
                  disabled={!onOpenVisit}
                  className="flex w-full items-center gap-2 py-1.5 text-left enabled:hover:bg-raised disabled:cursor-default"
                >
                  <Dot color={v.color} />
                  <span className="tnum shrink-0 text-[12px] text-ink">{v.local_date}</span>
                  <span className="truncate text-[12px] text-muted">{v.activity || '未分类'}</span>
                  <span className="tnum ml-auto shrink-0 text-[11px] text-faint">
                    {dur(v.duration_s)}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
