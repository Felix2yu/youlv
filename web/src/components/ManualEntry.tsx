import { useState } from 'react';
import { api } from '../lib/api';
import { localInputToUnix } from '../lib/format';
import type { Activity } from '../lib/types';
import { Button, ErrorBox } from './ui';

/**
 * 手动记一笔。
 *
 * 这个入口是补「只有导入」的结构性缺口：以前录错一条只能整批回滚重导，
 * 临时想补一段行程则完全没办法。表单刻意只留必需的几项——时间、地点名，
 * 其余（活动、备注、标签、坐标）都可空着，空着不影响入库。
 */
export default function ManualEntry({
  activities,
  defaultDate,
  onClose,
  onDone
}: {
  activities: Activity[];
  defaultDate: string;
  onClose: () => void;
  onDone: () => void;
}) {
  const [name, setName] = useState('');
  const [arrived, setArrived] = useState(() => `${defaultDate}T09:00`);
  const [minutes, setMinutes] = useState('60');
  const [activity, setActivity] = useState('');
  const [note, setNote] = useState('');
  const [tags, setTags] = useState('');
  const [city, setCity] = useState('');
  const [coord, setCoord] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function submit() {
    setErr(null);
    const arrivedAt = localInputToUnix(arrived);
    if (!arrivedAt) {
      setErr('先填抵达时间');
      return;
    }
    const trimmed = name.trim();
    if (!trimmed) {
      setErr('地点名不能为空');
      return;
    }
    let lat: number | undefined;
    let lon: number | undefined;
    if (coord.trim()) {
      // 「31.23, 121.47」这种写法比两个输入框顺手，粘贴自地图也常见。
      const m = coord
        .split(/[,，\s]+/)
        .filter(Boolean)
        .map(Number);
      if (m.length !== 2 || !m.every((n) => Number.isFinite(n))) {
        setErr('坐标要写成「纬度, 经度」，例如 31.2304, 121.4737');
        return;
      }
      [lat, lon] = m;
    }
    const mins = Number(minutes);
    const departedAt = mins > 0 ? arrivedAt + Math.round(mins * 60) : undefined;

    setBusy(true);
    try {
      await api.createVisit({
        place_name: trimmed,
        arrived_at: arrivedAt,
        departed_at: departedAt,
        activity,
        note,
        city: city.trim(),
        tags: tags
          .split(/[,，\s]+/)
          .map((t) => t.trim())
          .filter(Boolean),
        lat,
        lon
      });
      onDone();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 z-30 flex items-start justify-center overflow-y-auto p-4 sm:items-center">
      <button className="absolute inset-0 bg-black/25" onClick={onClose} aria-label="关闭" />
      <div
        className="relative w-full max-w-md rounded-[var(--radius-card)] border border-line bg-surface p-3 shadow-xl"
        role="dialog"
        aria-modal="true"
      >
        <div className="mb-2 flex items-baseline justify-between">
          <h2 className="text-[13px] font-semibold">手动记一笔</h2>
          <button
            onClick={onClose}
            className="rounded px-1.5 py-0.5 text-[12px] text-muted hover:bg-raised hover:text-ink"
          >
            取消
          </button>
        </div>

        <div className="flex flex-col gap-2">
          <Field label="地点">
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="例如：楼下那家面馆"
              autoFocus
              className={input}
            />
          </Field>

          <div className="grid grid-cols-2 gap-2">
            <Field label="抵达时间">
              <input
                type="datetime-local"
                value={arrived}
                onChange={(e) => setArrived(e.target.value)}
                className={input}
              />
            </Field>
            <Field label="停留（分钟）">
              <input
                type="number"
                min={0}
                value={minutes}
                onChange={(e) => setMinutes(e.target.value)}
                className={input}
              />
            </Field>
          </div>

          <div className="grid grid-cols-2 gap-2">
            <Field label="活动">
              <select
                value={activity}
                onChange={(e) => setActivity(e.target.value)}
                className={input}
              >
                <option value="">未分类</option>
                {activities.map((a) => (
                  <option key={a.name} value={a.name}>
                    {a.name}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="城市">
              <input
                value={city}
                onChange={(e) => setCity(e.target.value)}
                placeholder="可留空"
                className={input}
              />
            </Field>
          </div>

          <Field label="坐标" hint="选填，填了才会上地图">
            <input
              value={coord}
              onChange={(e) => setCoord(e.target.value)}
              placeholder="31.2304, 121.4737"
              className={input}
            />
          </Field>

          <Field label="标签" hint="空格或逗号分隔">
            <input
              value={tags}
              onChange={(e) => setTags(e.target.value)}
              placeholder="可选"
              className={input}
            />
          </Field>

          <Field label="备注">
            <textarea
              value={note}
              onChange={(e) => setNote(e.target.value)}
              rows={2}
              placeholder="可选"
              className={`${input} h-auto resize-none py-1.5`}
            />
          </Field>

          {err ? <ErrorBox>{err}</ErrorBox> : null}

          <div className="mt-1 flex items-center justify-end gap-2">
            <span className="mr-auto text-[11px] text-faint">
              时间按东八区记，与导入的数据同一口径
            </span>
            <Button variant="ghost" onClick={onClose}>
              取消
            </Button>
            <Button variant="primary" onClick={submit} disabled={busy}>
              {busy ? '保存中' : '保存'}
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}

const input =
  'h-8 w-full rounded-md border border-line-strong bg-surface px-2 text-[13px] text-ink placeholder:text-faint';

function Field({
  label,
  hint,
  children
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-[11px] text-faint">
        {label}
        {hint ? <span className="ml-1 text-faint/80">· {hint}</span> : null}
      </span>
      {children}
    </label>
  );
}
