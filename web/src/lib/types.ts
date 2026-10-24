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
  extra?: string[];
}

export interface ReportTotals {
  rows: number;
  ready: number;
  failed: number;
  dup_skip: number;
  inserted: number;
  skipped: number;
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
