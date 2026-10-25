import { useState } from 'react';
import { api } from '../lib/api';
import { useAsync } from '../lib/hooks';
import { dur, hhmm, temp, unixToLocalInput, localInputToUnix } from '../lib/format';
import type { Activity, VisitDetail } from '../lib/types';
import { Badge, Button, Dot, ErrorBox, Row, Spinner } from './ui';

/**
 * 到访详情抽屉。
 *
 * 为什么是抽屉而不是独立页面：应用只有六个 hash 路由，加第七个要动导航、
 * 路由表与深链规则三处；而抽屉能保留「从时间轴某条点进来」的上下文，
 * 关掉即回到原处。深链由调用方把 ?visit=<id> 写进 hash 实现。
 *
 * 详情里同时是唯一能改单条记录的地方：备注、活动、标签、离开时间都能改，
 * 也可以整条删掉。以前这些只能在 CSV 里改完重导，等于没有编辑能力。
 */
export default function VisitDrawer({
  id,
  onClose,
  onChanged
}: {
  id: number | null;
  onClose: () => void;
  onChanged?: () => void;
}) {
  const detail = useAsync<VisitDetail | null>(
    () => (id ? api.visit(id) : Promise.resolve(null)),
    [id]
  );
  const weather = useAsync(
    () =>
      id && detail.data
        ? api.weather(detail.data.place_id, detail.data.arrived_at)
        : Promise.resolve(null),
    [id, detail.data?.place_id, detail.data?.arrived_at]
  );
  const activities = useAsync<Activity[]>(() => api.activities(), []);

  if (id === null) return null;

  return (
    <div className="fixed inset-0 z-20 flex justify-end" role="dialog" aria-modal="true">
      <button className="absolute inset-0 bg-black/25" onClick={onClose} aria-label="关闭详情" />
      <aside className="relative flex h-full w-full max-w-sm flex-col overflow-y-auto border-l border-line bg-surface shadow-xl">
        <header className="sticky top-0 flex items-center gap-2 border-b border-line bg-surface px-3 py-2">
          <span className="text-[13px] font-semibold">到访详情</span>
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
            <div className="py-8 text-center text-[13px] text-muted">没有这条到访</div>
          ) : (
            <Detail
              d={detail.data}
              weather={weather.data}
              activities={activities.data ?? []}
              onSaved={() => {
                detail.reload();
                onChanged?.();
              }}
              onDeleted={() => {
                onClose();
                onChanged?.();
              }}
            />
          )}
        </div>
      </aside>
    </div>
  );
}

function Detail({
  d,
  weather,
  activities,
  onSaved,
  onDeleted
}: {
  d: VisitDetail;
  weather: { temp_c?: number; condition?: string; level?: string } | null;
  activities: Activity[];
  onSaved: () => void;
  onDeleted: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [confirmDel, setConfirmDel] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function remove() {
    setBusy(true);
    setErr(null);
    try {
      await api.deleteVisit(d.id);
      onDeleted();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <Dot color={d.color} />
            <span className="text-[15px] font-semibold">{d.display_name || d.place_name}</span>
          </div>
          <div className="mt-0.5 text-[12px] text-muted">
            {[d.city, d.district].filter(Boolean).join(' ') || '未标注城市'}
          </div>
        </div>
        <Button size="sm" variant="ghost" onClick={() => setEditing((v) => !v)}>
          {editing ? '收起' : '编辑'}
        </Button>
      </div>

      {editing ? (
        <EditForm
          d={d}
          activities={activities}
          onCancel={() => setEditing(false)}
          onSaved={() => {
            setEditing(false);
            onSaved();
          }}
        />
      ) : null}

      <div className="flex flex-wrap items-center gap-1.5">
        <Badge tone="accent">{d.activity || '未分类'}</Badge>
        {d.coord_frame === 'gcj02_mislabeled' ? (
          <Badge tone="warn" title="这行到访的原始坐标疑似未做坐标系换算，已自动纠正">
            坐标已纠正
          </Badge>
        ) : null}
        {d.resolve_confidence === 'low' ? (
          <Badge tone="warn" title="到访坐标距最近同名地点较远，绑定置信度低">
            绑定不确定
          </Badge>
        ) : null}
        {d.tags?.map((t) => (
          <Badge key={t}>{t}</Badge>
        ))}
      </div>

      <div>
        <Row k="抵达" v={`${d.local_date} ${hhmm(d.arrived_at)}`} />
        <Row k="离开" v={d.departed_at ? hhmm(d.departed_at) : '未记录'} />
        <Row k="停留" v={dur(d.duration_s)} />
        <Row k="星期" v={['周日', '周一', '周二', '周三', '周四', '周五', '周六'][d.local_dow]} />
        {weather ? (
          <Row
            k="天气"
            v={
              <>
                {weather.temp_c != null ? temp(weather.temp_c) : '—'}
                {weather.condition ? ` ${weather.condition}` : ''}
                {weather.level && weather.level !== 'exact' ? (
                  <span className="ml-1 text-[11px] text-warn">近似</span>
                ) : null}
              </>
            }
          />
        ) : null}
      </div>

      {d.note ? (
        <div className="rounded-md border border-line bg-raised px-2 py-1.5 text-[12px] leading-relaxed text-muted">
          {d.note}
        </div>
      ) : null}

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
            '这条到访没有坐标'
          )}
        </div>
        {d.lat_raw != null && d.lat != null && d.lat_raw !== d.lat ? (
          <div className="tnum mt-1 text-[11px] text-faint">
            原始值 {d.lat_raw.toFixed(6)}, {d.lon_raw?.toFixed(6)}
          </div>
        ) : null}
      </div>

      <div>
        <div className="mb-1 text-[11px] text-faint">解析依据</div>
        <Row k="绑定方式" v={d.resolve_method || '—'} />
        <Row k="距离" v={d.resolve_dist_m != null ? `${d.resolve_dist_m.toFixed(2)} m` : '—'} />
        <Row k="置信度" v={d.resolve_confidence || '—'} />
        <Row k="坐标系" v={d.coord_frame || '—'} />
      </div>

      {d.import_batch_no ? (
        <div className="text-[11px] text-faint">
          来自导入批次 <span className="tnum">{d.import_batch_no}</span>
        </div>
      ) : null}

      {err ? <ErrorBox>{err}</ErrorBox> : null}

      <div className="mt-1 flex items-center gap-2 border-t border-line pt-2">
        {confirmDel ? (
          <>
            <span className="text-[12px] text-danger">删掉就找不回来了，确定？</span>
            <Button size="sm" variant="ghost" onClick={() => setConfirmDel(false)} disabled={busy}>
              再想想
            </Button>
            <Button size="sm" variant="danger" onClick={remove} disabled={busy}>
              {busy ? '删除中' : '确认删除'}
            </Button>
          </>
        ) : (
          <Button size="sm" variant="plain" onClick={() => setConfirmDel(true)}>
            删除这条到访
          </Button>
        )}
      </div>
    </div>
  );
}

/** 单条编辑。只开放「改起来不会破坏统计口径」的几个字段。 */
function EditForm({
  d,
  activities,
  onCancel,
  onSaved
}: {
  d: VisitDetail;
  activities: Activity[];
  onCancel: () => void;
  onSaved: () => void;
}) {
  const [activity, setActivity] = useState(d.activity ?? '');
  const [note, setNote] = useState(d.note ?? '');
  const [tags, setTags] = useState((d.tags ?? []).join(' '));
  const [departed, setDeparted] = useState(unixToLocalInput(d.departed_at));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function save() {
    setErr(null);
    let dep: number | undefined;
    if (departed) {
      const v = localInputToUnix(departed);
      if (v == null) {
        setErr('离开时间格式不对');
        return;
      }
      if (v <= d.arrived_at) {
        setErr('离开时间要晚于抵达时间');
        return;
      }
      dep = v;
    }
    setBusy(true);
    try {
      await api.updateVisit(d.id, {
        activity,
        note,
        tags: tags
          .split(/[,，\s]+/)
          .map((t) => t.trim())
          .filter(Boolean),
        // 空串表示「清除离开时间」——后端 patch 里 null 才是不改。
        departed_at: dep ?? 0
      });
      onSaved();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  const input =
    'h-8 w-full rounded-md border border-line-strong bg-surface px-2 text-[13px] text-ink placeholder:text-faint';

  return (
    <div className="flex flex-col gap-2 rounded-md border border-line bg-raised/50 p-2">
      <div className="grid grid-cols-2 gap-2">
        <label className="flex flex-col gap-1">
          <span className="text-[11px] text-faint">活动</span>
          <select value={activity} onChange={(e) => setActivity(e.target.value)} className={input}>
            <option value="">未分类</option>
            {activities.map((a) => (
              <option key={a.name} value={a.name}>
                {a.name}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-[11px] text-faint">离开时间</span>
          <input
            type="datetime-local"
            value={departed}
            onChange={(e) => setDeparted(e.target.value)}
            className={input}
          />
        </label>
      </div>
      <label className="flex flex-col gap-1">
        <span className="text-[11px] text-faint">标签 · 空格分隔</span>
        <input value={tags} onChange={(e) => setTags(e.target.value)} className={input} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="text-[11px] text-faint">备注</span>
        <textarea
          value={note}
          onChange={(e) => setNote(e.target.value)}
          rows={2}
          className={`${input} h-auto resize-none py-1.5`}
        />
      </label>
      {err ? <ErrorBox>{err}</ErrorBox> : null}
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={onCancel} disabled={busy}>
          取消
        </Button>
        <Button size="sm" variant="primary" onClick={save} disabled={busy}>
          {busy ? '保存中' : '保存'}
        </Button>
      </div>
    </div>
  );
}
