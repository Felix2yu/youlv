// 后端 JSON 的 TS 镜像。字段名一律跟 Go 的 json tag 对齐，不在前端改名——
// 改名会让「接口改了字段」这件事在类型检查里静默溜过去。

export type Severity = 'info' | 'warn' | 'error';

export interface Meta {
  has_data: boolean;
  visits: number;
  places: number;
  trips: number;
  weather: number;
  cities: number;
  activities: number;
  first_date: string;
  last_date: string;
  schema_version: number;
}

export interface Visit {
  id: number;
  place_id: number;
  place_name: string;
  city?: string;
  activity: string;
  color: string;
  arrived_at: number;
  departed_at?: number;
  duration_s?: number;
  arrived_local: string;
  departed_local?: string;
  local_date: string;
  lat?: number;
  lon?: number;
  lat_gcj?: number;
  lon_gcj?: number;
  note?: string;
  tags?: string[];
  /** 抵达时刻的天气，由后端三级降级匹配而来 */
  temp_c?: number;
  condition?: string;
  weather_level?: string;
}

export interface DayBucket {
  date: string;
  count: number;
  duration_s: number;
  places: number;
}

export interface Place {
  id: number;
  name: string;
  display_name: string;
  lat?: number;
  lon?: number;
  lat_gcj?: number;
  lon_gcj?: number;
  city?: string;
  admin_area?: string;
  district?: string;
  street?: string;
  origin: string;
  is_stub: boolean;
  visit_count: number;
  first_visit_at?: number;
  last_visit_at?: number;
}

/** 地点详情：地点本身 + 最近到访 + 累计停留。 */
export interface PlaceDetail extends Place {
  recent: Visit[];
  duration_s: number;
}

/** 一段交通。时间轴里「怎么去的」来自这里。 */
export interface Trip {
  id: number;
  mode: string;
  mode_raw: string;
  from_at: number;
  to_at: number;
  from_local: string;
  to_local: string;
  local_date: string;
  duration_s: number;
  from_place: string;
  to_place: string;
  from_lat?: number;
  from_lon?: number;
  to_lat?: number;
  to_lon?: number;
  from_place_id: number;
  to_place_id: number;
}

/** 交通方式的中文说法：库里存的是英文枚举，界面上不能裸着。 */
export const MODE_CN: Record<string, string> = {
  walk: '步行',
  bike: '骑车',
  bus: '公交',
  metro: '地铁',
  train: '火车',
  car: '驾车',
  taxi: '出租车',
  plane: '飞机',
  ferry: '轮渡',
  other: '其他'
};

export interface Activity {
  name: string;
  color: string;
  icon: string;
  sort_order: number;
}

export interface ActivityStat {
  name: string;
  color: string;
  count: number;
  duration_s: number;
}

export interface CityStat {
  city: string;
  province: string;
  places: number;
  visits: number;
  duration_s: number;
  first_visit_at?: number;
}

export interface ModeStat {
  mode: string;
  count: number;
  duration_s: number;
}

export interface HourStat {
  hour: number;
  dow: number;
  n: number;
}

export interface MapPoint {
  lat: number;
  lon: number;
  lat_gcj: number;
  lon_gcj: number;
  n: number;
  duration_s: number;
  name: string;
  place_id: number;
}

export interface BBox {
  west: number;
  south: number;
  east: number;
  north: number;
  /** 参与计算范围的到访数。只有 0 才代表「没有带坐标的数据」——
   *  只有一个点时 west===east，不能拿它当空库判据。 */
  count?: number;
}

export interface Overview {
  visits: number;
  places: number;
  trips: number;
  cities: number;
  days: number;
  total_duration_s: number;
  first_date: string;
  last_date: string;
}

export interface Weather {
  place_name: string;
  hour_local: string;
  condition?: string;
  temp_c?: number;
  feels_like_c?: number;
  humidity?: number;
  wind_dir?: string;
  wind_speed_ms?: number;
  uv_index?: number;
  /** exact | nearby_hour | nearby_place | none：后端降级的档位，UI 要标注「近似」 */
  level?: string;
}

// ---------- 导入 ----------

export type ImportKind = 'places' | 'visits' | 'trips' | 'weather';

export interface Issue {
  /** 问题所属的文件类型（places|visits|trips|weather） */
  kind?: string;
  line: number;
  row_no: number;
  column: string;
  field: string;
  value: string;
  severity: Severity;
  code: string;
  message: string;
  hint: string;
}

export interface FieldDiff {
  field: string;
  label: string;
  old: unknown;
  new: unknown;
}

export interface ConflictGroup {
  key: string;
  kind: ImportKind;
  place_key: string;
  place_name: string;
  hour: string;
  diffs: FieldDiff[];
  old_raw: string;
  new_raw: string;
}

export interface FileStat {
  kind: ImportKind;
  label: string;
  file: string;
  sha256: string;
  rows: number;
  ready: number;
  failed: number;
  warned: number;
  dup_skip: number;
  inserted: number;
  skipped: number;
  detected: string;
  confidence: number;
  missing?: string[];
  /** 认识但没提供的列：不阻断导入，但对应数据会静默变空，所以要回显 */
  missing_opt?: string[];
  /** 这份文件实际按哪种编码解码：utf-8 / gb18030 / unknown */
  encoding?: string;
  extra?: string[];
}

export interface ReportTotals {
  rows: number;
  ready: number;
  failed: number;
  dup_skip: number;
  inserted: number;
  skipped: number;
  /** 仅引用桩升级为正式地点的条数（就地升级，不计入 inserted） */
  promoted: number;
}

export interface Options {
  timezone: string;
  dup_strategy: string;
  weather_dup_strategy: string;
  departed_before_arrived: string;
  create_stubs: boolean;
  require_places: boolean;
  rating_zero_null: boolean;
  radius_zero_null: boolean;
  snap_meters: number;
  nearest_meters: number;
}

export interface Report {
  batch_no: string;
  status: string;
  options: Options;
  files: FileStat[];
  issues: Issue[];
  conflicts: ConflictGroup[];
  highlights: string[];
  blocking: string[];
  totals: ReportTotals;
  duration_ms?: number;
}

/** 清空全部数据的产出：删掉了多少行，事后可核对。 */
export interface PurgeResult {
  deleted: {
    visits: number;
    trips: number;
    weather: number;
    places: number;
    tags: number;
    activities: number;
    batches: number;
    issues: number;
  };
}

export interface DetectItem {
  file: string;
  kind: string;
  label: string;
  confidence: number;
  rows: number;
  missing: string[];
  blocking: boolean;
}

export interface Decisions {
  global_strategy?: string;
  weather_strategy?: string;
  weather_overrides?: Record<string, string>;
}

export interface ImportRow {
  id: number;
  batch_no: string;
  status: string;
  rows_total: number;
  rows_inserted: number;
  rows_skipped: number;
  rows_failed: number;
  created_at: number;
  finished_at?: number;
  duration_ms?: number;
  error?: string;
}

export interface RollbackResult {
  batch_no: string;
  deleted: Record<string, number>;
}

// ---------- 详情与设置 ----------

/** 到访详情。比时间轴上的 Visit 多出解析依据、原始坐标与所属批次。 */
export interface VisitDetail {
  id: number;
  place_id: number;
  place_name: string;
  place_name_raw: string;
  display_name: string;
  city?: string;
  admin_area?: string;
  district?: string;
  activity: string;
  activity_raw: string;
  color: string;
  arrived_at: number;
  departed_at?: number;
  duration_s?: number;
  tz: string;
  arrived_local: string;
  departed_local?: string;
  local_date: string;
  local_month: string;
  local_year: number;
  local_hour: number;
  local_dow: number;
  lat?: number;
  lon?: number;
  lat_gcj?: number;
  lon_gcj?: number;
  lat_raw?: number;
  lon_raw?: number;
  coord_frame: string;
  resolve_method: string;
  resolve_dist_m?: number;
  resolve_confidence: string;
  note?: string;
  tags?: string[];
  import_batch_id?: number;
  import_batch_no?: string;
}

export interface Tag {
  id: number;
  name: string;
  color: string;
  count: number;
  first_visit_at?: number;
  last_visit_at?: number;
}

export interface PlaceStats {
  total: number;
  with_coord: number;
  without_coord: number;
  stubs: number;
  by_origin: Record<string, number>;
  cities: number;
  top_cities: CityStat[];
}

export interface TilesConfig {
  url_template: string;
  attribution: string;
  max_zoom: number;
  subdomains: string;
  frame: string;
}

export interface Settings {
  map: {
    tiles_url: string;
    tiles_attribution: string;
    tiles_max_zoom: number;
    tiles_subdomains: string;
    frame: string;
  };
  timeline: { frame: string };
  weather: { approx: boolean };
}
