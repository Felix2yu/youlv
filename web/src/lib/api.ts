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
  PlaceStats,
  Report,
  RollbackResult,
  PurgeResult,
  Settings,
  BBox,
  Tag,
  TilesConfig,
  Visit,
  VisitDetail,
  Weather,
  ActivityStat
} from './types';

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
  const res = await fetch(path, {
    ...init,
    headers: { accept: 'application/json', ...(init?.headers ?? {}) }
  });
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

  activities: () => json<{ items: Activity[] }>('/api/activities').then((r) => r.items),

  overview: () => json<Overview>('/api/stats/overview'),
  statsActivities: (from?: string, to?: string) =>
    json<{ items: ActivityStat[] }>(`/api/stats/activities${qs({ from, to })}`).then(
      (r) => r.items
    ),
  statsCities: () => json<{ items: CityStat[] }>('/api/stats/cities').then((r) => r.items),
  statsCommute: () => json<{ items: ModeStat[] }>('/api/stats/commute').then((r) => r.items),
  statsHourly: () => json<{ items: HourStat[] }>('/api/stats/hourly').then((r) => r.items),

  mapPoints: (b: BBox) =>
    json<{ items: MapPoint[] }>(
      `/api/map/points${qs({ west: b.west, south: b.south, east: b.east, north: b.north })}`
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

  commit: (batchNo: string, d: Decisions = {}) =>
    json<Report>(`/api/imports/${encodeURIComponent(batchNo)}/commit`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(d)
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
    const res = await fetch(`/api/imports/${encodeURIComponent(batchNo)}`, { method: 'DELETE' });
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
