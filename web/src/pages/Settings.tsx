import { useState } from 'react';
import { api } from '../lib/api';
import { useAsync, useTheme, type Theme } from '../lib/hooks';
import { num, ymd } from '../lib/format';
import type { ImportRow, Meta } from '../lib/types';
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
              <li
                key={r.batch_no}
                className="flex flex-wrap items-center gap-2 rounded border border-line px-2 py-1.5"
              >
                <span className="tnum text-[12px] text-muted">{ymd(r.created_at)}</span>
                <Badge tone={r.status === 'success' ? 'accent' : r.status === 'failed' ? 'danger' : 'neutral'}>
                  {STATUS_TEXT[r.status] ?? r.status}
                </Badge>
                <span className="tnum text-[11px] text-faint">{r.batch_no}</span>
                <span className="tnum ml-auto text-[11px] text-muted">
                  {num(r.rows_total)} 行 · 新增 {num(r.rows_inserted)} · 跳过 {num(r.rows_skipped)}
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
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </Card>
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
