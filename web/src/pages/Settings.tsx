import { useState } from 'react';
import { api, getToken, setToken } from '../lib/api';
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

export default function Settings({
  meta,
  onDataChanged
}: {
  meta: Meta | null;
  onDataChanged?: () => void;
}) {
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
          导数不必走命令行：直接下载一份 CSV（含 WGS-84 与 GCJ-02 两套坐标）——
        </p>
        <div className="mt-1 flex flex-wrap items-center gap-2">
          <a
            href={api.exportVisitsUrl()}
            download
            className="inline-flex h-7 items-center rounded-md border border-line-strong bg-surface px-2 text-[12px] font-medium text-ink hover:bg-raised"
          >
            导出全部到访
          </a>
          <span className="text-[11px] text-faint">最多 5 万条</span>
        </div>
        <p className="mt-3 text-[12px] leading-relaxed text-muted">
          整库备份走命令行，它会在不锁库的情况下生成一份完整副本：
        </p>
        <pre className="mt-1 overflow-x-auto rounded border border-line bg-raised px-2 py-1.5 text-[11px] text-muted">
          youlv backup &gt; youlv-备份.db
        </pre>
      </Card>

      <AccessToken />

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

      <PurgeCard meta={meta} onDone={onDataChanged} />
    </div>
  );
}

/** 清空全部数据：不可撤销，所以必须先展开确认、再点确认。 */
function PurgeCard({ meta, onDone }: { meta: Meta | null; onDone?: () => void }) {
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // 只有确实有数据时才把危险按钮亮出来；加载中（meta 为 null）不当成空库。
  const empty =
    meta != null && !meta.has_data && meta.visits === 0 && meta.places === 0 && meta.trips === 0;

  async function run() {
    setBusy(true);
    setErr(null);
    try {
      await api.purge();
      setConfirming(false);
      onDone?.();
      // 清空之后最该做的事就是重新导入，直接把人送到导入页。
      window.location.hash = '#/import';
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <SectionTitle>清空数据</SectionTitle>
      {err ? <ErrorBox>{err}</ErrorBox> : null}
      <p className="text-[12px] leading-relaxed text-muted">
        把导入进来的数据和导入历史一起删掉，回到空库；主题、底图这些设置项保留。
        清空前想留底，先用上面那条 <code>youlv backup</code> 导一份。
      </p>
      {empty ? (
        <p className="mt-2 text-[12px] text-faint">库里现在没有数据。</p>
      ) : confirming ? (
        <div className="mt-3 rounded-md border border-danger/40 bg-danger-soft px-3 py-2">
          <p className="text-[12px] leading-relaxed text-danger">
            将删除 {num(meta?.visits ?? 0)} 次到访、{num(meta?.places ?? 0)} 个地点、
            {num(meta?.trips ?? 0)} 段交通、{num(meta?.weather ?? 0)}{' '}
            条天气，以及全部批次与问题记录。
            <b> 不可撤销。</b>
          </p>
          <div className="mt-2 flex gap-2">
            <Button variant="danger" disabled={busy} onClick={() => void run()}>
              {busy ? '清空中…' : '确认清空'}
            </Button>
            <Button variant="ghost" disabled={busy} onClick={() => setConfirming(false)}>
              取消
            </Button>
          </div>
        </div>
      ) : (
        <Button variant="danger" className="mt-3" onClick={() => setConfirming(true)}>
          清空全部数据…
        </Button>
      )}
    </Card>
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

/**
 * 访问令牌。
 *
 * 服务端设了 YOULV_TOKEN 之后，所有 /api/ 都要求带上它；前端把它存在本地，
 * 每次请求附在 X-Auth-Token 里。没设令牌时这一栏留着也没坏处——
 * 填了也只会被忽略，所以文案要说明「只在服务端开了鉴权时才需要」。
 */
function AccessToken() {
  const [val, setVal] = useState(() => getToken());
  const [saved, setSaved] = useState(false);

  return (
    <Card>
      <SectionTitle>访问令牌</SectionTitle>
      <p className="mb-2 text-[12px] leading-relaxed text-muted">
        服务端启动时设了 <code className="text-ink">YOULV_TOKEN</code>{' '}
        才需要填。令牌只保存在这台设备的浏览器里， 不会上传；填完刷新页面生效。
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <input
          type="password"
          value={val}
          onChange={(e) => {
            setVal(e.target.value);
            setSaved(false);
          }}
          placeholder="未设置"
          className="h-8 min-w-0 flex-1 rounded-md border border-line-strong bg-surface px-2 text-[13px] placeholder:text-faint"
        />
        <Button
          variant="primary"
          onClick={() => {
            setToken(val.trim());
            setSaved(true);
          }}
        >
          保存
        </Button>
        <Button
          variant="ghost"
          onClick={() => {
            setToken('');
            setVal('');
            setSaved(true);
          }}
        >
          清除
        </Button>
      </div>
      {saved ? (
        <div className="mt-2 text-[12px] text-muted">已保存，刷新页面后按新令牌访问。</div>
      ) : null}
    </Card>
  );
}
