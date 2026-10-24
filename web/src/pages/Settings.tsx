import { useState } from 'react';
import { api } from '../lib/api';
import { useAsync, useTheme, type Theme } from '../lib/hooks';
import { num, ymd } from '../lib/format';
import type { ImportRow, Meta, Report, Settings } from '../lib/types';
import { Badge, Button, Card, Empty, ErrorBox, SectionTitle, Spinner } from '../components/ui';

const THEMES: { v: Theme; label: string }[] = [
  { v: 'light', label: '浅色' },
  { v: 'dark', label: '深色' },
  { v: 'auto', label: '跟随系统' }
];

const STATUS_TEXT: Record<string, string> = {
  preview: '待确认',
  running: '进行中',
  success: '已完成',
  failed: '失败',
  rolled_back: '已撤销'
};

export default function Settings({ meta }: { meta: Meta | null }) {
  const [theme, setTheme] = useTheme();
  const list = useAsync<ImportRow[]>(() => api.imports(50), []);
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<string | null>(null);

  async function rollback(batchNo: string) {
    setBusy(batchNo);
    setErr(null);
    try {
      await api.rollback(batchNo);
      list.reload();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  async function removeBatch(batchNo: string) {
    setBusy(batchNo);
    setErr(null);
    try {
      await api.deleteImport(batchNo);
      if (expanded === batchNo) setExpanded(null);
      list.reload();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-3">
      <Card>
        <SectionTitle>外观</SectionTitle>
        <div className="flex gap-1.5">
          {THEMES.map((t) => (
            <button
              key={t.v}
              onClick={() => setTheme(t.v)}
              className={`rounded-md border px-3 py-1 text-[12px] ${
                theme === t.v
                  ? 'border-accent bg-accent-soft text-accent'
                  : 'border-line-strong bg-surface text-muted hover:text-ink'
              }`}
            >
              {t.label}
            </button>
          ))}
        </div>
      </Card>

      <MapSettings />

      <Card>
        <SectionTitle>数据</SectionTitle>
        <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-[12px] sm:grid-cols-3">
          <KV k="到访" v={num(meta?.visits ?? 0)} />
          <KV k="地点" v={num(meta?.places ?? 0)} />
          <KV k="交通" v={num(meta?.trips ?? 0)} />
          <KV k="天气" v={num(meta?.weather ?? 0)} />
          <KV k="城市" v={num(meta?.cities ?? 0)} />
          <KV k="活动类型" v={num(meta?.activities ?? 0)} />
        </dl>
        {meta?.first_date ? (
          <div className="mt-2 text-[12px] text-muted">
            覆盖 {meta.first_date} 至 {meta.last_date}
          </div>
        ) : null}
        <p className="mt-3 text-[12px] leading-relaxed text-muted">
          数据库是单个 SQLite 文件。备份请用命令行导出，它会在不锁库的情况下生成一份完整副本：
        </p>
        <pre className="mt-1 overflow-x-auto rounded border border-line bg-raised px-2 py-1.5 text-[11px] text-muted">
          youlv backup &gt; youlv-备份.db
        </pre>
      </Card>

      <Card>
        <SectionTitle aside={<span className="tnum">{list.data?.length ?? 0} 批</span>}>
          导入历史
        </SectionTitle>
        {err ? <ErrorBox>{err}</ErrorBox> : null}
        {list.loading ? (
          <Spinner />
        ) : (list.data ?? []).length === 0 ? (
          <Empty>还没有导入过数据</Empty>
        ) : (
          <ul className="flex flex-col gap-1">
            {(list.data ?? []).map((r) => (
              <li key={r.batch_no} className="rounded border border-line">
                <div className="flex flex-wrap items-center gap-2 px-2 py-1.5">
                  <button
                    onClick={() => setExpanded(expanded === r.batch_no ? null : r.batch_no)}
                    className="text-[12px] text-muted hover:text-ink"
                    title={expanded === r.batch_no ? '收起详情' : '展开详情'}
                  >
                    {expanded === r.batch_no ? '▾' : '▸'}
                  </button>
                  <span className="tnum text-[12px] text-muted">{ymd(r.created_at)}</span>
                  <Badge
                    tone={
                      r.status === 'success'
                        ? 'accent'
                        : r.status === 'failed'
                          ? 'danger'
                          : 'neutral'
                    }
                  >
                    {STATUS_TEXT[r.status] ?? r.status}
                  </Badge>
                  <span className="tnum text-[11px] text-faint">{r.batch_no}</span>
                  <span className="tnum ml-auto text-[11px] text-muted">
                    {num(r.rows_total)} 行 · 新增 {num(r.rows_inserted)} · 跳过{' '}
                    {num(r.rows_skipped)}
                  </span>
                  {r.status === 'success' ? (
                    <Button
                      size="sm"
                      variant="danger"
                      disabled={busy === r.batch_no}
                      onClick={() => void rollback(r.batch_no)}
                    >
                      {busy === r.batch_no ? '撤销中…' : '撤销'}
                    </Button>
                  ) : (
                    <Button
                      size="sm"
                      variant="plain"
                      disabled={busy === r.batch_no}
                      onClick={() => void removeBatch(r.batch_no)}
                      title="删除这一批的审计记录（不删业务数据）"
                    >
                      {busy === r.batch_no ? '删除中…' : '删记录'}
                    </Button>
                  )}
                </div>
                {expanded === r.batch_no ? <BatchDetail batchNo={r.batch_no} /> : null}
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  );
}

/** 地图与显示设置。 */
function MapSettings() {
  const set = useAsync<Settings>(() => api.settings(), []);
  const [err, setErr] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const patch = async (p: Partial<Settings>) => {
    setSaving(true);
    setErr(null);
    try {
      await api.updateSettings(p);
      set.reload();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card>
      <SectionTitle aside={saving ? <Spinner /> : undefined}>地图与显示</SectionTitle>
      {err ? <ErrorBox>{err}</ErrorBox> : null}
      {set.loading || !set.data ? (
        <Spinner />
      ) : (
        <div className="flex flex-col gap-3">
          <div>
            <label className="mb-1 block text-[12px] text-muted">底图瓦片地址</label>
            <input
              defaultValue={set.data.map.tiles_url}
              onBlur={(e) => {
                if (e.target.value !== set.data?.map.tiles_url) {
                  void patch({ map: { ...set.data!.map, tiles_url: e.target.value } });
                }
              }}
              placeholder="留空则只保留自绘层"
              className="h-8 w-full rounded-md border border-line-strong bg-surface px-2 text-[12px] placeholder:text-faint"
            />
            <p className="mt-1 text-[11px] text-faint">
              支持 {'{s}'} {'{z}'} {'{x}'} {'{y}'} 占位符。留空即不提供底图，只用自绘层。
            </p>
          </div>

          <div className="flex flex-wrap items-center gap-3">
            <label className="flex items-center gap-1.5 text-[12px] text-muted">
              地图坐标系
              <select
                value={set.data.map.frame}
                onChange={(e) => void patch({ map: { ...set.data!.map, frame: e.target.value } })}
                className="h-7 rounded-md border border-line-strong bg-surface px-1.5 text-[12px]"
              >
                <option value="wgs84">WGS-84</option>
                <option value="gcj02">GCJ-02（国内底图）</option>
              </select>
            </label>
            <label className="flex items-center gap-1.5 text-[12px] text-muted">
              <input
                type="checkbox"
                checked={set.data.weather.approx}
                onChange={(e) =>
                  void patch({
                    weather: { approx: e.target.checked }
                  })
                }
                className="size-3.5 accent-[var(--color-accent)]"
              />
              允许用邻近时刻的天气近似
            </label>
          </div>
        </div>
      )}
    </Card>
  );
}

/** 批次详情：文件级统计与问题清单。 */
function BatchDetail({ batchNo }: { batchNo: string }) {
  const rep = useAsync<Report>(() => api.report(batchNo), [batchNo]);
  const issues = useAsync(() => api.issues(batchNo), [batchNo]);

  if (rep.loading) {
    return (
      <div className="border-t border-line px-2 py-2">
        <Spinner />
      </div>
    );
  }
  if (rep.error) {
    return (
      <div className="border-t border-line px-2 py-2">
        <ErrorBox>{rep.error}</ErrorBox>
      </div>
    );
  }
  const r = rep.data;
  if (!r) return null;

  return (
    <div className="border-t border-line px-2 py-2">
      <div className="tnum mb-1 flex flex-wrap gap-x-3 gap-y-0.5 text-[11px] text-muted">
        <span>待导入 {num(r.totals.ready)}</span>
        <span>重复跳过 {num(r.totals.dup_skip)}</span>
        <span>写入 {num(r.totals.inserted)}</span>
        <span>失败 {num(r.totals.failed)}</span>
        {r.totals.promoted ? <span>桩升级 {num(r.totals.promoted)}</span> : null}
        <span>冲突 {num(r.conflicts.length)} 组</span>
      </div>

      {r.highlights.length ? (
        <ul className="mb-1 flex flex-col gap-0.5">
          {r.highlights.slice(0, 6).map((h, i) => (
            <li key={i} className="flex gap-1.5 text-[11px] leading-relaxed text-muted">
              <span className="mt-1 size-1 shrink-0 rounded-full bg-accent" />
              {h}
            </li>
          ))}
        </ul>
      ) : null}

      {issues.data ? (
        <div>
          <div className="tnum mb-1 text-[11px] text-faint">
            问题 {num(issues.data.total)} 条
            {issues.data.total > issues.data.items.length
              ? `（显示前 ${issues.data.items.length} 条）`
              : ''}
          </div>
          <ul className="flex max-h-48 flex-col gap-0.5 overflow-y-auto">
            {issues.data.items.slice(0, 50).map((it, i) => (
              <li key={i} className="flex gap-1.5 text-[11px] text-muted">
                <Badge
                  tone={
                    it.severity === 'error' ? 'danger' : it.severity === 'warn' ? 'warn' : 'neutral'
                  }
                >
                  {it.kind || '—'}
                </Badge>
                <span className="min-w-0 flex-1 truncate">
                  第 {it.line} 行 · {it.message}
                </span>
              </li>
            ))}
          </ul>
          <a
            className="mt-1 inline-block text-[11px] text-accent hover:underline"
            href={api.issuesCsvUrl(batchNo)}
            download
          >
            下载完整清单（CSV）
          </a>
        </div>
      ) : null}
    </div>
  );
}

function KV({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex items-baseline justify-between gap-2 border-b border-line py-0.5">
      <dt className="text-muted">{k}</dt>
      <dd className="tnum">{v}</dd>
    </div>
  );
}
