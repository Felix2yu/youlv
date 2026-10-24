import type { ButtonHTMLAttributes, ReactNode } from 'react';

// 一组够用的原语。不引组件库的原因很实际：这六个页面总共只需要卡片、
// 按钮、标签三种容器，引 MUI 之类的代价是几百 KB 与一套主题系统。

export function Card({
  children,
  className = '',
  padded = true
}: {
  children: ReactNode;
  className?: string;
  padded?: boolean;
}) {
  return (
    <div
      className={`rounded-[var(--radius-card)] border border-line bg-surface ${
        padded ? 'p-3' : ''
      } ${className}`}
    >
      {children}
    </div>
  );
}

export function SectionTitle({ children, aside }: { children: ReactNode; aside?: ReactNode }) {
  return (
    <div className="mb-2 flex items-baseline justify-between gap-3">
      <h2 className="text-[13px] font-semibold tracking-wide text-ink">{children}</h2>
      {aside ? <div className="text-[12px] text-faint">{aside}</div> : null}
    </div>
  );
}

type BtnProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: 'primary' | 'ghost' | 'danger' | 'plain';
  size?: 'sm' | 'md';
};

export function Button({ variant = 'ghost', size = 'md', className = '', ...rest }: BtnProps) {
  const base =
    'inline-flex items-center justify-center gap-1.5 rounded-md border font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-45';
  const sizes = {
    sm: 'h-7 px-2 text-[12px]',
    md: 'h-8 px-3 text-[13px]'
  }[size];
  const variants = {
    primary: 'border-transparent bg-accent text-white hover:brightness-110',
    ghost: 'border-line-strong bg-surface text-ink hover:bg-raised',
    danger: 'border-transparent bg-danger text-white hover:brightness-110',
    plain: 'border-transparent bg-transparent text-muted hover:bg-raised hover:text-ink'
  }[variant];
  return <button className={`${base} ${sizes} ${variants} ${className}`} {...rest} />;
}

export function Badge({
  children,
  tone = 'neutral',
  title
}: {
  children: ReactNode;
  tone?: 'neutral' | 'accent' | 'warn' | 'danger' | 'info';
  title?: string;
}) {
  const tones = {
    neutral: 'border-line-strong bg-raised text-muted',
    accent: 'border-accent-line bg-accent-soft text-accent',
    warn: 'border-warn/40 bg-warn-soft text-warn',
    danger: 'border-danger/40 bg-danger-soft text-danger',
    info: 'border-info/40 bg-info-soft text-info'
  }[tone];
  return (
    <span
      title={title}
      className={`inline-flex shrink-0 items-center rounded border px-1.5 py-px text-[11px] leading-[16px] ${tones}`}
    >
      {children}
    </span>
  );
}

/** 活动色点。后端给的是 hex，直接当背景色用。 */
export function Dot({ color, className = '' }: { color: string; className?: string }) {
  return (
    <span
      className={`inline-block size-2 shrink-0 rounded-full ${className}`}
      style={{ backgroundColor: color || 'var(--color-faint)' }}
    />
  );
}

export function Spinner({ label }: { label?: string }) {
  return (
    <div className="flex items-center gap-2 text-[13px] text-muted">
      <span className="size-3.5 animate-spin rounded-full border-2 border-line-strong border-t-accent" />
      {label ?? '加载中'}
    </div>
  );
}

export function Empty({ children, hint }: { children: ReactNode; hint?: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-1 py-10 text-center">
      <div className="text-[13px] text-muted">{children}</div>
      {hint ? <div className="max-w-sm text-[12px] leading-relaxed text-faint">{hint}</div> : null}
    </div>
  );
}

export function ErrorBox({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-md border border-danger/40 bg-danger-soft px-3 py-2 text-[13px] text-danger">
      {children}
    </div>
  );
}

/** 定义列表式的紧凑键值行。统计页与地点详情都在用。 */
export function Row({ k, v }: { k: ReactNode; v: ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-3 border-b border-line py-1.5 last:border-0">
      <span className="shrink-0 text-[12px] text-muted">{k}</span>
      <span className="tnum text-right text-[13px] text-ink">{v}</span>
    </div>
  );
}

/** 横向条形：统计页的主力图形。n 为 0 时不画，避免出现 1px 的残条。 */
export function Bar({
  value,
  max,
  color,
  height = 6
}: {
  value: number;
  max: number;
  color?: string;
  height?: number;
}) {
  const pct = max > 0 ? Math.max(value > 0 ? 2 : 0, (value / max) * 100) : 0;
  return (
    <div className="w-full overflow-hidden rounded-full bg-raised" style={{ height }}>
      <div
        className="h-full rounded-full transition-[width] duration-300"
        style={{ width: `${pct}%`, backgroundColor: color ?? 'var(--color-accent)' }}
      />
    </div>
  );
}
