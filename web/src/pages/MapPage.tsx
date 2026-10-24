import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api } from '../lib/api';
import { useAsync } from '../lib/hooks';
import { dur } from '../lib/format';
import { fitBounds, project, toScreen, viewBounds, type View } from '../lib/project';
import type { BBox, MapPoint, TilesConfig } from '../lib/types';
import { Badge, Button, Card, Empty, ErrorBox, Spinner } from '../components/ui';

type Layer = 'canvas' | 'leaflet';

export default function MapPage() {
  const [layer, setLayer] = useState<Layer>('canvas');
  const bounds = useAsync(() => api.mapBounds(), []);
  const tiles = useAsync(() => api.tiles(), []);
  const [selected, setSelected] = useState<MapPoint | null>(null);

  // 瓦片地址为空表示不提供底图（用户可以在设置里清空它），
  // 这时只留自绘层，不给一个点了没反应的按钮。
  const hasTiles = (tiles.data?.url_template ?? '') !== '';

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex rounded-md border border-line-strong">
          {(
            [['canvas', '自绘'], ...(hasTiles ? ([['leaflet', '底图']] as const) : [])] as const
          ).map(([v, label], i) => (
            <button
              key={v}
              onClick={() => setLayer(v)}
              className={`px-2.5 py-1 text-[12px] ${
                layer === v ? 'bg-accent text-white' : 'bg-surface text-muted hover:text-ink'
              } ${i === 0 ? 'rounded-l-md' : 'rounded-r-md'}`}
            >
              {label}
            </button>
          ))}
        </div>
        <span className="text-[11px] text-faint">
          {layer === 'canvas' ? '不请求任何外部瓦片，纯本地绘制' : '加载在线瓦片，需要能访问外网'}
        </span>
      </div>

      {bounds.error ? (
        <ErrorBox>{bounds.error}</ErrorBox>
      ) : bounds.loading || !bounds.data ? (
        <Card className="grid h-[70vh] place-items-center">
          <Spinner label="正在计算视野" />
        </Card>
      ) : bounds.data.east === bounds.data.west ? (
        <Card>
          <Empty hint="导入带坐标的到访记录后，这里会画出你去过的所有地方。">
            还没有带坐标的到访
          </Empty>
        </Card>
      ) : layer === 'canvas' ? (
        <CanvasMap bounds={bounds.data} selected={selected} onSelect={setSelected} />
      ) : tiles.data ? (
        <LeafletMap
          bounds={bounds.data}
          tiles={tiles.data}
          selected={selected}
          onSelect={setSelected}
        />
      ) : (
        <Card className="grid h-[70vh] place-items-center">
          <Spinner label="正在读取底图配置" />
        </Card>
      )}
    </div>
  );
}

// ---------------------------------------------------------------- 自绘层

function CanvasMap({
  bounds,
  selected,
  onSelect
}: {
  bounds: BBox;
  selected: MapPoint | null;
  onSelect: (p: MapPoint | null) => void;
}) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ w: 800, h: 520 });
  const [view, setView] = useState<View>(() =>
    fitBounds(
      { south: bounds.south, west: bounds.west, north: bounds.north, east: bounds.east },
      800,
      520
    )
  );
  const drag = useRef<{ x: number; y: number; cx: number; cy: number } | null>(null);

  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => {
      const r = el.getBoundingClientRect();
      setSize({ w: Math.max(320, r.width), h: Math.max(320, r.height) });
    });
    ro.observe(el);
    const r = el.getBoundingClientRect();
    setSize({ w: Math.max(320, r.width), h: Math.max(320, r.height) });
    return () => ro.disconnect();
  }, []);

  // 视野变化后取一次点位。k 与中心量化到 4 位，避免拖动中每像素都发请求。
  const key = `${view.cx.toFixed(4)},${view.cy.toFixed(4)},${view.k.toFixed(3)}`;
  const fetched = useAsync(() => {
    const b = viewBounds(view, size.w, size.h);
    return api.mapPoints({ west: b.west, south: b.south, east: b.east, north: b.north });
  }, [key, size.w, size.h]);
  // 用 useMemo 包一层：`?? []` 每次渲染都是新数组，会让下游 useMemo 的依赖
  // 每次都变，白白重算标签避让。
  const points = useMemo(() => fetched.data ?? [], [fetched.data]);
  const loading = fetched.loading;
  const err = fetched.error;

  const maxN = useMemo(() => Math.max(1, ...points.map((p) => p.n)), [points]);

  // 标签避让：按到访次数从多到少逐个占位，与已放置标签重叠的放弃。
  // 没有这一步，几十个点挤在一起的城市，标签会叠成一团黑。
  const labelIds = useMemo(() => {
    const cand = points.filter((p) => view.k > 40 || p.n / maxN > 0.6).sort((a, b) => b.n - a.n);
    const boxes: { x1: number; y1: number; x2: number; y2: number }[] = [];
    const ok = new Set<number>();
    for (const p of cand) {
      const s = toScreen(project(p.lat, p.lon), view, size.w, size.h);
      if (s.x < 0 || s.y < 0 || s.x > size.w || s.y > size.h) continue;
      const r = 3 + Math.sqrt(p.n / maxN) * 11;
      // 文本宽度按全角 11px 估，比 getBBox 便宜且不需要等 DOM 渲染完
      const w = [...p.name].reduce((a, ch) => a + (ch.charCodeAt(0) > 255 ? 11 : 6), 0) + 6;
      const box = { x1: s.x + r, y1: s.y - 8, x2: s.x + r + 3 + w, y2: s.y + 8 };
      if (boxes.some((b) => !(box.x2 < b.x1 || box.x1 > b.x2 || box.y2 < b.y1 || box.y1 > b.y2)))
        continue;
      boxes.push(box);
      ok.add(p.place_id);
    }
    return ok;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [points, view.cx, view.cy, view.k, size.w, size.h, maxN]);

  const reset = () =>
    setView(
      fitBounds(
        { south: bounds.south, west: bounds.west, north: bounds.north, east: bounds.east },
        size.w,
        size.h
      )
    );

  const onWheel = useCallback(
    (e: React.WheelEvent<SVGSVGElement>) => {
      e.preventDefault();
      const r = e.currentTarget.getBoundingClientRect();
      const mx = e.clientX - r.left;
      const my = e.clientY - r.top;
      // 以鼠标为锚：缩放前后，光标下的那个墨卡托点必须落在同一像素上。
      const before = {
        x: (mx - size.w / 2) / (view.k * size.w) + view.cx,
        y: (my - size.h / 2) / (view.k * size.w) + view.cy
      };
      const k2 = Math.min(4000, Math.max(0.9, view.k * Math.pow(1.0015, -e.deltaY)));
      const cx2 = before.x - (mx - size.w / 2) / (k2 * size.w);
      const cy2 = before.y - (my - size.h / 2) / (k2 * size.w);
      setView({ cx: cx2, cy: cy2, k: k2 });
    },
    [view, size]
  );

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <Button size="sm" onClick={reset}>
          回到全览
        </Button>
        <span className="tnum text-[11px] text-faint">
          {points.length} 个地点 · 缩放{' '}
          {view.k < 2 ? '1' : Math.round(view.k).toLocaleString('zh-CN')}×
        </span>
        {loading ? <Spinner /> : null}
        {err ? <span className="text-[11px] text-danger">{err}</span> : null}
      </div>
      <div className="flex gap-2">
        <Card padded={false} className="min-w-0 flex-1 overflow-hidden">
          <div ref={wrapRef} className="h-[62vh] min-h-[320px] w-full">
            <svg
              width={size.w}
              height={size.h}
              className="cursor-grab touch-none select-none active:cursor-grabbing"
              onWheel={onWheel}
              onPointerDown={(e) => {
                (e.target as Element).setPointerCapture?.(e.pointerId);
                drag.current = { x: e.clientX, y: e.clientY, cx: view.cx, cy: view.cy };
              }}
              onPointerMove={(e) => {
                if (!drag.current) return;
                const d = drag.current;
                setView((v) => ({
                  ...v,
                  cx: d.cx - (e.clientX - d.x) / (v.k * size.w),
                  cy: d.cy - (e.clientY - d.y) / (v.k * size.w)
                }));
              }}
              onPointerUp={() => {
                drag.current = null;
              }}
              onClick={() => onSelect(null)}
            >
              <rect width={size.w} height={size.h} fill="var(--color-surface)" />
              <Graticule view={view} w={size.w} h={size.h} />
              {points.map((p) => {
                const s = toScreen(project(p.lat, p.lon), view, size.w, size.h);
                if (s.x < -40 || s.y < -40 || s.x > size.w + 40 || s.y > size.h + 40) return null;
                const r = 3 + Math.sqrt(p.n / maxN) * 11;
                const on = selected?.place_id === p.place_id;
                return (
                  <g
                    key={p.place_id}
                    onClick={(e) => {
                      e.stopPropagation();
                      onSelect(p);
                    }}
                    className="cursor-pointer"
                  >
                    <circle
                      cx={s.x}
                      cy={s.y}
                      r={r}
                      fill="var(--color-accent)"
                      fillOpacity={0.28}
                      stroke={on ? 'var(--color-ink)' : 'var(--color-accent)'}
                      strokeWidth={on ? 2 : 1}
                    />
                    {labelIds.has(p.place_id) ? (
                      <text
                        x={s.x + r + 3}
                        y={s.y + 3.5}
                        fontSize={11}
                        fill="var(--color-ink)"
                        className="pointer-events-none"
                      >
                        {p.name}
                      </text>
                    ) : null}
                  </g>
                );
              })}
            </svg>
          </div>
        </Card>
        <aside className="hidden w-56 shrink-0 lg:block">
          {selected ? <PointCard p={selected} /> : null}
        </aside>
      </div>
      {selected ? (
        <div className="lg:hidden">
          <PointCard p={selected} />
        </div>
      ) : null}
    </div>
  );
}

/** 经纬网格。档位随缩放切换，否则放大后线会密成一片。 */
function Graticule({ view, w, h }: { view: View; w: number; h: number }) {
  const step = view.k > 60 ? 0.01 : view.k > 12 ? 0.05 : view.k > 3 ? 0.25 : 1;
  const lines: React.ReactElement[] = [];
  const push = (lat1: number, lon1: number, lat2: number, lon2: number, i: number) => {
    const a = toScreen(project(lat1, lon1), view, w, h);
    const b = toScreen(project(lat2, lon2), view, w, h);
    lines.push(
      <line
        key={i}
        x1={a.x}
        y1={a.y}
        x2={b.x}
        y2={b.y}
        stroke="var(--color-line)"
        strokeWidth={1}
      />
    );
  };
  let i = 0;
  for (let lon = -180; lon <= 180; lon += step) push(-85, lon, 85, lon, i++);
  for (let lat = -80; lat <= 80; lat += step) push(lat, -180, lat, 180, i++);
  return <g className="pointer-events-none">{lines}</g>;
}

function PointCard({ p }: { p: MapPoint }) {
  return (
    <Card>
      <div className="mb-1 flex items-baseline gap-2">
        <span className="min-w-0 flex-1 truncate text-[13px] font-medium">{p.name}</span>
        <Badge tone="accent">{p.n} 次</Badge>
      </div>
      <div className="text-[12px] text-muted">累计 {dur(p.duration_s)}</div>
      <div className="tnum mt-1 text-[11px] text-faint">
        {p.lat.toFixed(5)}, {p.lon.toFixed(5)}
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------- Leaflet 层

function LeafletMap({
  bounds,
  tiles,
  selected,
  onSelect
}: {
  bounds: BBox;
  tiles: TilesConfig;
  selected: MapPoint | null;
  onSelect: (p: MapPoint | null) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [err, setErr] = useState<string | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    let map: import('leaflet').Map | null = null;
    let cancelled = false;

    (async () => {
      try {
        const L = await import('leaflet');
        await import('leaflet/dist/leaflet.css');
        if (cancelled) return;
        map = L.map(el, { zoomControl: true, attributionControl: true });
        // 瓦片地址与子域都来自服务端设置：自托管用户可以指向自己的瓦片
        // 服务器（离线、内网、自建），不必改前端代码。
        L.tileLayer(tiles.url_template, {
          subdomains: tiles.subdomains
            .split(',')
            .map((s) => s.trim())
            .filter(Boolean),
          maxZoom: tiles.max_zoom,
          attribution: tiles.attribution
        }).addTo(map);
        map.fitBounds(L.latLngBounds([bounds.south, bounds.west], [bounds.north, bounds.east]), {
          padding: [24, 24]
        });
        const load = async () => {
          if (!map) return;
          const b = map.getBounds();
          const list = await api.mapPoints({
            west: b.getWest(),
            south: b.getSouth(),
            east: b.getEast(),
            north: b.getNorth()
          });
          if (cancelled) return;
          const g = L.layerGroup().addTo(map);
          const maxN = Math.max(1, ...list.map((p) => p.n));
          // 底图是 GCJ-02 的（高德/腾讯/国内图源）时，点位必须也用 GCJ-02，
          // 否则整体偏移三四百米。国内底图与 WGS-84 混用是这个应用最容易
          // 踩的坑，所以坐标系由设置决定而不是在前端写死。
          const useGCJ = tiles.frame === 'gcj02';
          for (const p of list) {
            const r = 4 + Math.sqrt(p.n / maxN) * 12;
            const lat = useGCJ ? p.lat_gcj : p.lat;
            const lon = useGCJ ? p.lon_gcj : p.lon;
            L.circleMarker([lat, lon], {
              radius: r,
              color: '#2f6b4f',
              weight: 1,
              fillColor: '#2f6b4f',
              fillOpacity: 0.3
            })
              .bindTooltip(`${p.name} · ${p.n} 次 · ${dur(p.duration_s)}`)
              .on('click', () => onSelect(p))
              .addTo(g);
          }
        };
        map.on('moveend', () => void load());
        await load();
        setReady(true);
      } catch (e) {
        if (!cancelled) setErr(e instanceof Error ? e.message : String(e));
      }
    })();

    return () => {
      cancelled = true;
      map?.remove();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="flex gap-2">
      <Card padded={false} className="min-w-0 flex-1 overflow-hidden">
        {err ? (
          <div className="p-3">
            <ErrorBox>底图加载失败：{err}</ErrorBox>
          </div>
        ) : !ready ? (
          <div className="grid h-[62vh] min-h-[320px] place-items-center">
            <Spinner label="正在加载底图" />
          </div>
        ) : null}
        <div ref={ref} className="h-[62vh] min-h-[320px] w-full" />
      </Card>
      <aside className="hidden w-56 shrink-0 lg:block">
        {selected ? <PointCard p={selected} /> : null}
      </aside>
    </div>
  );
}
