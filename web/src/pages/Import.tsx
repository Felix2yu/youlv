import { useEffect, useMemo, useRef, useState } from 'react';
import { ApiError, api } from '../lib/api';
import { bytes, num } from '../lib/format';
import type { DetectItem, Issue, Meta, Report } from '../lib/types';
import { Badge, Button, Card, Empty, ErrorBox, SectionTitle, Spinner } from '../components/ui';

type Step = 1 | 2 | 3 | 4 | 5 | 6;

const STEPS: { n: Step; label: string }[] = [
  { n: 1, label: '上传' },
  { n: 2, label: '识别' },
  { n: 3, label: '预检' },
  { n: 4, label: '裁决' },
  { n: 5, label: '执行' },
  { n: 6, label: '结果' }
];

export default function Import({
  meta,
  onDataChanged
}: {
  meta: Meta | null;
  onDataChanged?: () => void;
}) {
  const [step, setStep] = useState<Step>(1);
  const [files, setFiles] = useState<File[]>([]);
  // null 表示「识别还没有结果」：可能正在上传，也可能识别请求失败了。
  // 这两种状态都必须给用户出路，所以不能和「识别出 0 个文件」混为一谈。
  const [detected, setDetected] = useState<DetectItem[] | null>(null);
  const [report, setReport] = useState<Report | null>(null);
  const [committed, setCommitted] = useState<Report | null>(null);
  const [busy, setBusy] = useState(false);
  // 上传 + 识别单独一个开关：它和「预检中」语义不同，
  // 共用一个 busy 会让第 2 步的按钮文案自相矛盾。
  const [detecting, setDetecting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // 裁决：conflictKey -> 'old' | 'new'
  const [overrides, setOverrides] = useState<Record<string, string>>({});
  const inputRef = useRef<HTMLInputElement>(null);
  // 拖拽中（用于给虚线框加高亮）。整页可放，所以状态挂在页面级别。
  const [dragging, setDragging] = useState(false);
  // 拖放监听只挂一次，但要用到「当前这一步」和「最新的 onDetect」，故走 ref。
  const stepRef = useRef(step);
  const detectRef = useRef(onDetect);
  // 写入过程中的中止器：导入大文件要一会儿，得给个出口。
  const abortRef = useRef<AbortController | null>(null);
  // 每次渲染后同步最新值；在渲染期写 ref 会被 react-hooks 规则拦下。
  useEffect(() => {
    stepRef.current = step;
    detectRef.current = onDetect;
  });

  // 拖放监听挂在 window 上而不是那只虚线框上。两个原因：
  // 1. 虚线框只有 100 多像素高，拖到卡片标题、说明文字或文件列表上就落到框外，
  //    浏览器会按默认行为直接打开/下载 CSV —— 页面看起来「拖了没反应」；
  // 2. 只有拦住默认行为，才能保证拖歪了也不会把当前页面顶掉。
  useEffect(() => {
    const isFileDrag = (e: DragEvent) => Array.from(e.dataTransfer?.types ?? []).includes('Files');

    const onDragOver = (e: DragEvent) => {
      if (!isFileDrag(e)) return;
      e.preventDefault();
      if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
      setDragging(true);
    };
    const onDragLeave = (e: DragEvent) => {
      // relatedTarget 为 null 才是真的离开窗口；在子元素之间移动时不清高亮，免得闪。
      if (e.relatedTarget === null) setDragging(false);
    };
    const onDrop = (e: DragEvent) => {
      if (!isFileDrag(e)) return;
      e.preventDefault();
      setDragging(false);
      const list = pickCsvFiles(e.dataTransfer);
      if (!list.length) {
        setError('只认 CSV / TXT。Rond 导出的是四份 .csv 文件，文件夹和压缩包不行。');
        return;
      }
      if (stepRef.current === 1) void detectRef.current(list);
      else setError('当前已经在导入流程里。要换文件，请先回到第 1 步点「重选文件」。');
    };

    window.addEventListener('dragover', onDragOver);
    window.addEventListener('dragleave', onDragLeave);
    window.addEventListener('drop', onDrop);
    return () => {
      window.removeEventListener('dragover', onDragOver);
      window.removeEventListener('dragleave', onDragLeave);
      window.removeEventListener('drop', onDrop);
    };
  }, []);

  const blocking = useMemo(() => (detected ?? []).filter((d) => d.blocking), [detected]);

  async function onDetect(list: File[]) {
    // 选完文件立刻进第 2 步。上传 + 识别 1~2 MB 也要一两秒，若停在第 1 步
    // 什么都不显示，用户只会以为「选完没反应」—— 这正是最容易被当成卡死的地方。
    setFiles(list);
    setDetected(null);
    setError(null);
    setStep(2);
    setDetecting(true);
    try {
      const items = await api.detect(list);
      setDetected(items);
    } catch (e) {
      setError(detectErrorText(e));
    } finally {
      setDetecting(false);
    }
  }

  async function onPreview() {
    setError(null);
    setBusy(true);
    try {
      const rep = await api.preview(files);
      setReport(rep);
      // 默认沿用已导入的值，所以初始不写任何覆盖。
      setOverrides({});
      setStep(3);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function onCommit() {
    if (!report) return;
    setError(null);
    setBusy(true);
    // 先切到「执行中」这一步再发请求：导入 1 万多行要一两秒，
    // 让用户盯着裁决页的按钮等，是最容易被当成卡死的地方。
    setStep(5);
    const ac = new AbortController();
    abortRef.current = ac;
    try {
      const rep = await api.commit(
        report.batch_no,
        { weather_strategy: 'old', weather_overrides: overrides },
        ac.signal
      );
      setCommitted(rep);
      setStep(6);
      onDataChanged?.();
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') {
        // 用户自己取消的不算错误，回到裁决页让他决定下一步。
        setStep(4);
        return;
      }
      setError(e instanceof Error ? e.message : String(e));
      setStep(4);
    } finally {
      abortRef.current = null;
      setBusy(false);
    }
  }

  async function onRollback() {
    if (!committed) return;
    setError(null);
    setBusy(true);
    try {
      await api.rollback(committed.batch_no);
      setCommitted(null);
      setReport(null);
      setStep(1);
      onDataChanged?.();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-3">
      <StepBar step={step} onJump={(n) => n < step && setStep(n)} />
      {error ? <ErrorBox>{error}</ErrorBox> : null}

      {step === 1 ? (
        <Card>
          <SectionTitle aside={files.length ? `${files.length} 个文件` : undefined}>
            选择 Rond 导出的 CSV
          </SectionTitle>
          <p className="mb-3 text-[12px] leading-relaxed text-muted">
            把四份导出文件一起拖到页面上——地点记录、到访记录、交通记录、天气数据。
            缺哪一份也能导入，只是对应的功能会空着。
          </p>
          <div
            onClick={() => inputRef.current?.click()}
            className={`grid cursor-pointer place-items-center gap-1 rounded-md border border-dashed px-4 py-10 text-center transition-colors ${
              dragging
                ? 'border-accent bg-accent-soft'
                : 'border-line-strong bg-raised hover:border-accent'
            }`}
          >
            <div className="text-[13px] text-ink">
              {dragging ? '松手就开始识别' : '拖到页面任意位置，或点击选择'}
            </div>
            <div className="text-[11px] text-faint">
              CSV 文件，单个不超过 64 MB · 选完就开始识别
            </div>
          </div>
          <input
            ref={inputRef}
            type="file"
            multiple
            accept=".csv,text/csv"
            className="hidden"
            onChange={(e) => {
              const list = Array.from(e.target.files ?? []);
              // 不清空 value，重新选同一批文件不会再触发 change ——
              // 「再试一次」必须真的能再试一次。
              e.target.value = '';
              if (list.length) void onDetect(list);
            }}
          />
          {files.length ? (
            <ul className="mt-3 flex flex-col gap-1">
              {files.map((f) => (
                <li
                  key={f.name}
                  className="flex items-center gap-2 rounded border border-line px-2 py-1 text-[12px]"
                >
                  <span className="min-w-0 flex-1 truncate">{f.name}</span>
                  <span className="tnum shrink-0 text-faint">{bytes(f.size)}</span>
                </li>
              ))}
            </ul>
          ) : null}
          {meta && !meta.has_data ? (
            <p className="mt-3 text-[12px] text-muted">
              库里还没有数据。导入完成后，时间轴、足迹地图和统计就都能用了。
            </p>
          ) : null}
        </Card>
      ) : null}

      {step === 2 ? (
        <Card>
          <SectionTitle aside={files.length ? `${files.length} 个文件` : undefined}>
            识别结果
          </SectionTitle>
          {detecting ? (
            <div className="flex flex-col items-center gap-2 py-8">
              <Spinner label={`正在上传并识别 ${files.length} 个文件…`} />
              <p className="max-w-sm text-center text-[12px] leading-relaxed text-muted">
                四份 CSV 合计通常一两 MB，正常一两秒出结果。
              </p>
            </div>
          ) : detected === null ? (
            <p className="mb-3 text-[12px] leading-relaxed text-muted">
              识别没成功。这不影响导入——预检会把这几份文件重新读一遍，给出完整的行数与问题清单。
            </p>
          ) : (
            <ul className="mb-3 flex flex-col gap-1">
              {detected.map((d) => (
                <li
                  key={d.file}
                  className="flex items-center gap-2 rounded border border-line px-2 py-1.5 text-[13px]"
                >
                  <span className="min-w-0 flex-1 truncate">{d.file}</span>
                  {d.blocking ? (
                    <Badge tone="danger">无法识别</Badge>
                  ) : (
                    <>
                      <Badge tone="accent">{d.label}</Badge>
                      <span className="tnum text-[11px] text-faint">{d.rows} 行</span>
                    </>
                  )}
                </li>
              ))}
            </ul>
          )}
          {detected !== null && blocking.length ? (
            <ErrorBox>
              有 {blocking.length} 个文件认不出类型（缺少必需列），请确认是不是 Rond 的导出文件。
            </ErrorBox>
          ) : null}
          <div className="mt-3 flex gap-2">
            <Button
              variant="ghost"
              disabled={busy || detecting}
              onClick={() => {
                setDetected(null);
                setError(null);
                setStep(1);
              }}
            >
              重选文件
            </Button>
            <Button
              variant="primary"
              disabled={busy || detecting || blocking.length > 0 || files.length === 0}
              onClick={() => void onPreview()}
            >
              {detecting ? '识别中…' : busy ? '预检中…' : `预检 ${files.length} 个文件`}
            </Button>
          </div>
        </Card>
      ) : null}

      {step === 3 && report ? (
        <PreviewStep report={report} onNext={() => setStep(4)} onBack={() => setStep(2)} />
      ) : null}

      {step === 4 && report ? (
        <ConflictStep
          report={report}
          overrides={overrides}
          setOverrides={setOverrides}
          onNext={() => void onCommit()}
          onBack={() => setStep(3)}
          busy={busy}
        />
      ) : null}

      {step === 5 && report ? (
        <Card>
          <SectionTitle>正在写入</SectionTitle>
          <div className="flex flex-col items-center gap-2 py-8">
            <Spinner label={`正在写入 ${num(report.totals.ready)} 行`} />
            <p className="max-w-sm text-center text-[12px] leading-relaxed text-muted">
              整批数据在**一个事务**里写入：中途任何一处出错都会整体回滚，
              不会出现「地点进了、到访没进」的半截状态。
            </p>
            <p className="tnum text-[11px] text-faint">批次 {report.batch_no}</p>
            <Button
              variant="ghost"
              onClick={() => abortRef.current?.abort()}
              disabled={!abortRef.current}
            >
              取消导入
            </Button>
          </div>
        </Card>
      ) : null}

      {step === 6 && committed ? (
        <ResultStep committed={committed} onRollback={() => void onRollback()} busy={busy} />
      ) : null}
    </div>
  );
}

function StepBar({ step, onJump }: { step: Step; onJump: (n: Step) => void }) {
  return (
    <div className="flex items-center gap-1 overflow-x-auto">
      {STEPS.map((s, i) => {
        const done = s.n < step;
        const active = s.n === step;
        return (
          <div key={s.n} className="flex items-center gap-1">
            {i > 0 ? <span className="h-px w-3 bg-line-strong" /> : null}
            <button
              disabled={!done}
              onClick={() => onJump(s.n)}
              className={`flex items-center gap-1 rounded px-1.5 py-0.5 text-[12px] ${
                active
                  ? 'bg-accent-soft font-medium text-accent'
                  : done
                    ? 'text-muted hover:bg-raised'
                    : 'text-faint'
              }`}
            >
              <span
                className={`tnum grid size-4 place-items-center rounded-full text-[10px] ${
                  active ? 'bg-accent text-white' : done ? 'bg-accent-line text-white' : 'bg-raised'
                }`}
              >
                {done ? '✓' : s.n}
              </span>
              {s.label}
            </button>
          </div>
        );
      })}
    </div>
  );
}

function PreviewStep({
  report,
  onNext,
  onBack
}: {
  report: Report;
  onNext: () => void;
  onBack: () => void;
}) {
  const errs = report.issues.filter((i) => i.severity === 'error');
  const warns = report.issues.filter((i) => i.severity === 'warn');

  return (
    <>
      <Card>
        <SectionTitle
          aside={
            <span className="tnum">{report.duration_ms ? `${report.duration_ms} ms` : '—'}</span>
          }
        >
          预检结果
        </SectionTitle>
        <div className="mb-3 grid grid-cols-2 gap-2 sm:grid-cols-4">
          <Stat label="总行数" value={num(report.totals.rows)} />
          <Stat label="待导入" value={num(report.totals.ready)} tone="accent" />
          <Stat label="重复跳过" value={num(report.totals.dup_skip)} />
          <Stat
            label="无法导入"
            value={num(report.totals.failed)}
            tone={errs.length ? 'danger' : undefined}
          />
        </div>
        <table className="w-full text-[12px]">
          <thead className="text-faint">
            <tr className="border-b border-line">
              <th className="py-1 text-left font-normal">文件</th>
              <th className="py-1 text-right font-normal">行数</th>
              <th className="py-1 text-right font-normal">待导入</th>
              <th className="py-1 text-right font-normal">跳过</th>
              <th className="py-1 text-right font-normal">失败</th>
            </tr>
          </thead>
          <tbody className="tnum">
            {report.files.map((f) => (
              <tr key={f.file} className="border-b border-line last:border-0">
                <td className="py-1">
                  {f.label}
                  {/* 缺可选列不阻断导入，但对应数据会静默变空，
                      不点出来用户会以为是自己数错了。 */}
                  {f.missing_opt?.length ? (
                    <span className="ml-1 text-[11px] text-faint">
                      缺 {f.missing_opt.join('、')}
                    </span>
                  ) : null}
                </td>
                <td className="py-1 text-right">{f.rows}</td>
                <td className="py-1 text-right">{f.ready}</td>
                <td className="py-1 text-right">{f.dup_skip}</td>
                <td className="py-1 text-right">{f.failed}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>

      {report.highlights.length ? (
        <Card>
          <SectionTitle>需要注意</SectionTitle>
          <ul className="flex flex-col gap-1.5">
            {report.highlights.map((h, i) => (
              <li key={i} className="flex gap-2 text-[12px] leading-relaxed text-muted">
                <span className="mt-1.5 size-1 shrink-0 rounded-full bg-accent" />
                {h}
              </li>
            ))}
          </ul>
        </Card>
      ) : null}

      {errs.length || warns.length ? (
        <Card>
          <SectionTitle
            aside={
              <a
                className="text-accent hover:underline"
                href={api.issuesCsvUrl(report.batch_no)}
                download
              >
                下载完整清单（CSV）
              </a>
            }
          >
            问题明细
          </SectionTitle>
          <div className="mb-2 flex gap-2">
            {errs.length ? <Badge tone="danger">{errs.length} 条错误</Badge> : null}
            {warns.length ? <Badge tone="warn">{warns.length} 条提醒</Badge> : null}
          </div>
          <IssueList issues={[...errs, ...warns].slice(0, 200)} />
          {errs.length + warns.length > 200 ? (
            <div className="mt-2 text-[11px] text-faint">只显示前 200 条，完整清单请下载 CSV。</div>
          ) : null}
        </Card>
      ) : null}

      {report.totals.ready === 0 && report.conflicts.length === 0 ? (
        // 灰掉按钮不够：用户面对一个不可点的按钮，并不知道是「全重复了」
        // 还是「出错了」。重复导入本来就该是 ready=0，这是正常结果，要讲明白。
        <p className="rounded-lg border border-line bg-raised px-3 py-2 text-[12px] leading-relaxed text-muted">
          这 {num(report.totals.dup_skip)} 行库里都已经有了，没有新数据要导入。
          {report.totals.failed > 0
            ? `另有 ${num(report.totals.failed)} 行无法导入，见下方问题明细。`
            : '可以直接返回，也可以换一份导出文件再试。'}
        </p>
      ) : null}
      <div className="flex gap-2">
        <Button variant="ghost" onClick={onBack}>
          返回
        </Button>
        <Button
          variant="primary"
          onClick={onNext}
          disabled={report.totals.ready === 0 && report.conflicts.length === 0}
        >
          {report.conflicts.length ? `去裁决 ${report.conflicts.length} 组冲突` : '直接导入'}
        </Button>
      </div>
    </>
  );
}

function IssueList({ issues }: { issues: Issue[] }) {
  const byCode = useMemo(() => {
    const m = new Map<string, { code: string; severity: string; n: number; sample: Issue }>();
    for (const i of issues) {
      const cur = m.get(i.code);
      if (cur) cur.n += 1;
      else m.set(i.code, { code: i.code, severity: i.severity, n: 1, sample: i });
    }
    return [...m.values()].sort((a, b) => b.n - a.n);
  }, [issues]);

  return (
    <ul className="flex flex-col gap-1">
      {byCode.map((g) => (
        <li key={g.code} className="rounded border border-line px-2 py-1.5">
          <div className="flex items-center gap-2">
            <Badge
              tone={g.severity === 'error' ? 'danger' : g.severity === 'warn' ? 'warn' : 'neutral'}
            >
              {g.n} 条
            </Badge>
            <span className="text-[12px] font-medium">{g.sample.message}</span>
          </div>
          <div className="mt-0.5 text-[11px] text-faint">
            例：第 {g.sample.line} 行 · {g.sample.column} · {g.sample.hint}
          </div>
        </li>
      ))}
    </ul>
  );
}

function ConflictStep({
  report,
  overrides,
  setOverrides,
  onNext,
  onBack,
  busy
}: {
  report: Report;
  overrides: Record<string, string>;
  setOverrides: (o: Record<string, string>) => void;
  onNext: () => void;
  onBack: () => void;
  busy: boolean;
}) {
  const [pick, setPick] = useState<Record<string, 'old' | 'new'>>({});
  const conflicts = report.conflicts;

  return (
    <>
      <Card>
        <SectionTitle aside={<span className="tnum">{conflicts.length} 组</span>}>
          天气冲突裁决
        </SectionTitle>
        <p className="mb-3 text-[12px] leading-relaxed text-muted">
          同一地点、同一小时在导出里出现了两份不一样的天气。默认保留<b>已导入</b>的那份；
          想换成新数据，就在下面逐组改。
        </p>
        <div className="mb-3 flex gap-2">
          <Button
            size="sm"
            onClick={() => {
              const next: Record<string, 'old' | 'new'> = {};
              const ov: Record<string, string> = {};
              for (const c of conflicts) {
                next[c.key] = 'old';
                ov[c.key] = 'old';
              }
              setPick(next);
              setOverrides(ov);
            }}
          >
            全部保留已导入
          </Button>
          <Button
            size="sm"
            onClick={() => {
              const next: Record<string, 'old' | 'new'> = {};
              const ov: Record<string, string> = {};
              for (const c of conflicts) {
                next[c.key] = 'new';
                ov[c.key] = 'new';
              }
              setPick(next);
              setOverrides(ov);
            }}
          >
            全部用新数据
          </Button>
        </div>
        {conflicts.length === 0 ? (
          <Empty>没有需要裁决的冲突</Empty>
        ) : (
          <ul className="flex max-h-96 flex-col gap-2 overflow-y-auto pr-1">
            {conflicts.map((c) => (
              <li key={c.key} className="rounded border border-line px-2 py-1.5">
                <div className="mb-1 flex items-baseline gap-2">
                  <span className="text-[12px] font-medium">{c.place_name}</span>
                  <span className="tnum text-[11px] text-faint">{c.hour}</span>
                  <div className="ml-auto flex gap-1">
                    {(['old', 'new'] as const).map((side) => (
                      <button
                        key={side}
                        onClick={() => {
                          setPick((p) => ({ ...p, [c.key]: side }));
                          setOverrides({ ...overrides, [c.key]: side });
                        }}
                        className={`rounded px-1.5 py-0.5 text-[11px] ${
                          (pick[c.key] ?? 'old') === side
                            ? 'bg-accent text-white'
                            : 'bg-raised text-muted hover:text-ink'
                        }`}
                      >
                        {side === 'old' ? '已导入' : '新数据'}
                      </button>
                    ))}
                  </div>
                </div>
                <table className="w-full text-[11px]">
                  <tbody>
                    {c.diffs.map((d) => (
                      <tr key={d.field} className="border-t border-line">
                        <td className="py-0.5 text-faint">{d.label}</td>
                        <td className="tnum py-0.5 text-right">{fmt(d.old)}</td>
                        <td className="tnum py-0.5 text-right">{fmt(d.new)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </li>
            ))}
          </ul>
        )}
      </Card>
      <div className="flex gap-2">
        <Button variant="ghost" onClick={onBack} disabled={busy}>
          返回
        </Button>
        <Button variant="primary" onClick={onNext} disabled={busy}>
          {busy ? '正在导入…' : `导入 ${num(report.totals.ready)} 行`}
        </Button>
      </div>
    </>
  );
}

/** 从拖放数据里取 CSV。优先 files，取不到再退回 items ——
 *  Safari 拖文件夹等来源时 files 会是空的，只看 files 会漏掉。 */
function pickCsvFiles(dt: DataTransfer | null): File[] {
  if (!dt) return [];
  const all: File[] = dt.files.length
    ? Array.from(dt.files)
    : Array.from(dt.items ?? [])
        .filter((it) => it.kind === 'file')
        .map((it) => it.getAsFile())
        .filter((f): f is File => f != null);
  return all.filter((f) => /\.(csv|txt)$/i.test(f.name));
}

/** 把识别接口的异常翻成一句用户能照着做的话。 */
function detectErrorText(e: unknown): string {
  if (e instanceof DOMException && e.name === 'TimeoutError') {
    return '上传或识别 90 秒没有响应。网络慢或服务一时没响应时可以重选文件重试，也可以直接点预检。';
  }
  if (e instanceof ApiError) return e.message;
  return e instanceof Error ? e.message : String(e);
}

function fmt(v: unknown): string {
  if (v == null) return '—';
  if (typeof v === 'number') {
    return Number.isInteger(v) ? String(v) : v.toFixed(2);
  }
  return String(v);
}

function ResultStep({
  committed,
  onRollback,
  busy
}: {
  committed: Report;
  onRollback: () => void;
  busy: boolean;
}) {
  return (
    <>
      <Card>
        <SectionTitle aside={<span className="tnum">{committed.duration_ms ?? 0} ms</span>}>
          导入完成
        </SectionTitle>
        <div className="mb-3 grid grid-cols-2 gap-2 sm:grid-cols-4">
          <Stat label="写入" value={num(committed.totals.inserted)} tone="accent" />
          <Stat label="跳过" value={num(committed.totals.skipped + committed.totals.dup_skip)} />
          <Stat label="失败" value={num(committed.totals.failed)} />
          <Stat label="总行数" value={num(committed.totals.rows)} />
        </div>
        <ul className="flex flex-col gap-1">
          {committed.files.map((f) => (
            <li key={f.file} className="flex items-center gap-2 text-[12px]">
              <span className="min-w-0 flex-1 truncate">{f.label}</span>
              <span className="tnum text-muted">
                新增 {f.inserted} · 跳过 {f.skipped + f.dup_skip}
              </span>
            </li>
          ))}
        </ul>
      </Card>
      <Card>
        <SectionTitle>后悔药</SectionTitle>
        <p className="mb-2 text-[12px] leading-relaxed text-muted">
          这一批写进去的数据可以整批撤销。只删本批次写入的记录，其它批次不受影响。
        </p>
        <Button variant="danger" onClick={onRollback} disabled={busy}>
          {busy ? '撤销中…' : `撤销批次 ${committed.batch_no}`}
        </Button>
      </Card>
    </>
  );
}

function Stat({
  label,
  value,
  tone
}: {
  label: string;
  value: string;
  tone?: 'accent' | 'danger';
}) {
  const color = tone === 'accent' ? 'text-accent' : tone === 'danger' ? 'text-danger' : 'text-ink';
  return (
    <div className="rounded-md border border-line bg-raised px-2 py-1.5">
      <div className="text-[11px] text-faint">{label}</div>
      <div className={`tnum text-[15px] font-semibold ${color}`}>{value}</div>
    </div>
  );
}
