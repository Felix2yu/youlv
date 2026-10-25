import type {
  Activity,
  CityStat,
  DayBucket,
  Decisions,
  DetectItem,
  HourStat,
  ImportRow,
  MapPoint,
  Meta,
  ModeStat,
  Overview,
  Place,
  PlaceDetail,
  PlaceStats,
  Report,
  RollbackResult,
  PurgeResult,
  Settings,
  BBox,
  Tag,
  TilesConfig,
  Trip,
  Visit,
  VisitDetail,
  Weather,
  ActivityStat
} from './types';

// 访问令牌存在本地：服务端设了 YOULV_TOKEN 时，前端必须带上它才能取数。
// 存 localStorage 而不是让用户每次输，是因为这是单人自托管应用，
// 「打开就能用」比「每次开页面输一次令牌」重要。
const TOKEN_KEY = 'youlv.token';

export function getToken(): string {
  try {
    return localStorage.getItem(TOKEN_KEY) ?? '';
  } catch {
    return '';
  }
}

export function setToken(t: string): void {
  try {
    if (t) localStorage.setItem(TOKEN_KEY, t);
    else localStorage.removeItem(TOKEN_KEY);
  } catch {
    // 隐私模式下写不了，本次会话仍可用内存里的值，忽略
  }
}

function authHeaders(extra?: HeadersInit): Record<string, string> {
  const token = getToken();
  return {
    accept: 'application/json',
    ...(token ? { 'X-Auth-Token': token } : {}),
    ...((extra as Record<string, string> | undefined) ?? {})
  };
}

// 后端对错误的响应统一是 {"error": "..."}。
export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

async function json<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, { ...init, headers: authHeaders(init?.headers) });
  } catch (e) {
    // fetch 只在网络层失败时 reject（服务没起、断网、被中断），此时抛的是
    // 浏览器原生的 TypeError，英文文案（"Failed to fetch"）会直接出现在中文
    // 界面上。这里统一翻掉。
    if (e instanceof DOMException && e.name === 'AbortError') throw e;
    throw new Error('连不上服务，检查一下它还跑着吗');
  }
  const text = await res.text();
  let body: unknown = null;
  if (text) {
    try {
      body = JSON.parse(text);
    } catch {
      // 健康检查之类返回非 JSON 时不必硬解，交给调用方按 text 处理
    }
  }
  if (!res.ok) {
    // 401 单独翻译：用户看到「缺少或无效的访问令牌」得知道去哪儿补。
    if (res.status === 401) {
      throw new ApiError('服务端要求访问令牌：请到「设置」里填一下', 401);
    }
    const msg =
      body && typeof body === 'object' && 'error' in body
        ? String((body as { error: unknown }).error)
        : `请求失败（${res.status}）`;
    throw new ApiError(msg, res.status);
  }
  return body as T;
}

function qs(params: Record<string, string | number | boolean | undefined | null>): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === '') continue;
    sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : '';
}

export const api = {
  meta: () => json<Meta>('/api/meta'),

  timelineDay: (date: string, frame = 'wgs84') =>
    json<{ items: Visit[] }>(`/api/timeline/day${qs({ date, frame })}`).then((r) => r.items),

  timelineMonth: (month: string) =>
    json<{ days: DayBucket[] }>(`/api/timeline/month${qs({ month })}`).then((r) => r.days),

  timelineRange: (p: {
    from?: string;
    to?: string;
    q?: string;
    activity?: string;
    place_id?: number;
    limit?: number;
    offset?: number;
    frame?: string;
  }) => json<{ items: Visit[]; total: number }>(`/api/timeline/range${qs({ ...p })}`),

  places: (p: {
    q?: string;
    city?: string;
    origin?: string;
    sort?: string;
    has_coord?: boolean;
    limit?: number;
    offset?: number;
  }) =>
    json<{ items: Place[]; total: number }>(
      `/api/places${qs({ ...p, has_coord: p.has_coord ? '1' : '' })}`
    ),

  place: (id: number, frame = 'wgs84') => json<PlaceDetail>(`/api/places/${id}${qs({ frame })}`),

  placeVisits: (id: number, limit = 50, frame = 'wgs84') =>
    json<{ items: Visit[] }>(`/api/places/${id}/visits${qs({ limit, frame })}`).then(
      (r) => r.items
    ),

  trips: (p: { from?: string; to?: string; mode?: string; limit?: number } = {}) =>
    json<{ items: Trip[]; total: number }>(`/api/trips${qs({ ...p })}`),

  // 导出走浏览器直接下载，<a href> 带不上请求头，令牌只能放 query。
  exportVisitsUrl: (p: { from?: string; to?: string } = {}) =>
    `/api/export/visits.csv${qs({ ...p, token: getToken() })}`,

  // 手动录入：不经过 CSV，直接记一条到访。
  createVisit: (b: {
    place_name?: string;
    place_id?: number;
    arrived_at: number;
    departed_at?: number;
    activity?: string;
    note?: string;
    tags?: string[];
    lat?: number;
    lon?: number;
    city?: string;
  }) =>
    json<{ visit_id: number; place_id: number; created_place: boolean }>('/api/visits', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(b)
    }),

  updateVisit: (
    id: number,
    b: { note?: string; activity?: string; tags?: string[]; departed_at?: number }
  ) =>
    json<{ ok: boolean }>(`/api/visits/${id}`, {
      method: 'PATCH',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(b)
    }),

  deleteVisit: (id: number) =>
    fetch(`/api/visits/${id}`, { method: 'DELETE', headers: authHeaders() }).then((r) => {
      if (!r.ok) throw new ApiError('删除失败', r.status);
    }),

  activities: () => json<{ items: Activity[] }>('/api/activities').then((r) => r.items),

  overview: () => json<Overview>('/api/stats/overview'),
  statsActivities: (from?: string, to?: string) =>
    json<{ items: ActivityStat[] }>(`/api/stats/activities${qs({ from, to })}`).then(
      (r) => r.items
    ),
  statsCities: () => json<{ items: CityStat[] }>('/api/stats/cities').then((r) => r.items),
  statsCommute: () => json<{ items: ModeStat[] }>('/api/stats/commute').then((r) => r.items),
  statsHourly: () => json<{ items: HourStat[] }>('/api/stats/hourly').then((r) => r.items),

  // frame 要与底图一致：GCJ-02 底图上的视口边界也是 GCJ-02 的，
  // 传 wgs84 会让视野边缘的点位被裁掉。
  mapPoints: (b: BBox, frame = 'wgs84') =>
    json<{ items: MapPoint[] }>(
      `/api/map/points${qs({ west: b.west, south: b.south, east: b.east, north: b.north, frame })}`
    ).then((r) => r.items),
  mapBounds: () => json<BBox>('/api/map/bounds'),

  weather: (placeId: number, at?: number, approx = true) =>
    json<Weather>(
      `/api/weather${qs({ place_id: placeId, at: at || '', approx: approx ? '' : '0' })}`
    ),

  // ---- 导入 ----
  // 识别只是「回显类型」，不是导入的必经步骤，所以给它一个硬超时：
  // 上传卡住时宁可报超时让用户直接走预检，也不要把人无限期按在转圈上。
  detect: (files: File[]) => {
    const fd = new FormData();
    for (const f of files) fd.append('files', f, f.name);
    return json<{ items: DetectItem[] }>('/api/imports/detect', {
      method: 'POST',
      body: fd,
      signal: AbortSignal.timeout(90_000)
    }).then((r) => r.items);
  },

  preview: (files: File[], signal?: AbortSignal) => {
    const fd = new FormData();
    for (const f of files) fd.append('files', f, f.name);
    return json<Report>('/api/imports/preview', { method: 'POST', body: fd, signal });
  },

  // signal 让长导入可以中途取消：写入一万多行要一两秒到几十秒，
  // 没有取消路径，用户只能干等着或刷新页面。
  commit: (batchNo: string, d: Decisions = {}, signal?: AbortSignal) =>
    json<Report>(`/api/imports/${encodeURIComponent(batchNo)}/commit`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(d),
      signal
    }),

  rollback: (batchNo: string) =>
    json<RollbackResult>(`/api/imports/${encodeURIComponent(batchNo)}/rollback`, {
      method: 'POST'
    }),

  // 清空全部导入数据与批次记录（不可撤销）。confirm 是后端要求的显式确认标记。
  purge: () =>
    json<PurgeResult>('/api/imports/purge', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ confirm: 'PURGE' })
    }),

  imports: (limit = 50) =>
    json<{ items: ImportRow[] }>(`/api/imports${qs({ limit })}`).then((r) => r.items),

  report: (batchNo: string) => json<Report>(`/api/imports/${encodeURIComponent(batchNo)}`),

  issues: (batchNo: string) =>
    json<{ items: import('./types').Issue[]; total: number }>(
      `/api/imports/${encodeURIComponent(batchNo)}/issues`
    ),

  issuesCsvUrl: (batchNo: string) => `/api/imports/${encodeURIComponent(batchNo)}/issues.csv`,

  // ---- 详情与设置 ----
  visit: (id: number, frame = 'wgs84') => json<VisitDetail>(`/api/visits/${id}${qs({ frame })}`),

  tags: (limit = 100) => json<{ items: Tag[] }>(`/api/tags${qs({ limit })}`).then((r) => r.items),

  placesStats: (p: { city?: string; origin?: string } = {}) =>
    json<PlaceStats>(`/api/places/stats${qs({ ...p })}`),

  tiles: () => json<TilesConfig>('/api/map/tiles'),

  settings: () => json<Settings>('/api/settings'),

  updateSettings: (patch: Partial<Settings>) =>
    json<Settings>('/api/settings', {
      method: 'PUT',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(patch)
    }),

  deleteImport: async (batchNo: string) => {
    const res = await fetch(`/api/imports/${encodeURIComponent(batchNo)}`, {
      method: 'DELETE',
      headers: authHeaders()
    });
    if (!res.ok) {
      const body = await res.text();
      let msg = `删除失败（${res.status}）`;
      try {
        const j = JSON.parse(body) as { error?: string };
        if (j.error) msg = j.error;
      } catch {
        // 非 JSON 错误体，用默认文案
      }
      throw new ApiError(msg, res.status);
    }
  }
};
