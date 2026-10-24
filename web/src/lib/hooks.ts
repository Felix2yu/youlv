import { useCallback, useEffect, useState } from 'react';

// ---------- 主题 ----------

export type Theme = 'light' | 'dark' | 'auto';

const THEME_KEY = 'youlv.theme';

function systemPrefersDark(): boolean {
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false;
}

/** 把主题落到 <html> 的 class 上。auto 跟随系统并订阅变化。 */
export function useTheme(): [Theme, (t: Theme) => void] {
  const [theme, setTheme] = useState<Theme>(
    () => (localStorage.getItem(THEME_KEY) as Theme | null) ?? 'auto'
  );

  useEffect(() => {
    localStorage.setItem(THEME_KEY, theme);
    const dark = theme === 'dark' || (theme === 'auto' && systemPrefersDark());
    document.documentElement.classList.toggle('dark', dark);
  }, [theme]);

  useEffect(() => {
    if (theme !== 'auto') return;
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const onChange = () => document.documentElement.classList.toggle('dark', mq.matches);
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
  }, [theme]);

  return [theme, setTheme];
}

// ---------- 路由 ----------

const ROUTE_KEY = 'youlv.route';

function parseHash(): { path: string; query: URLSearchParams } {
  const raw = window.location.hash.replace(/^#/, '');
  const i = raw.indexOf('?');
  const path = (i < 0 ? raw : raw.slice(0, i)) || '/';
  const query = new URLSearchParams(i < 0 ? '' : raw.slice(i + 1));
  return { path, query };
}

/**
 * 极简 hash 路由。
 *
 * 不引路由库的原因：这是个单页自托管应用，页面只有六个，而 React Router
 * 带来的是一整套 loader / 嵌套路由心智模型。hash 方案还顺带解决了
 * 「Go 静态托管只有一层 SPA 回退」这件事——深链刷新不会 404。
 */
export function useRoute(): {
  path: string;
  query: URLSearchParams;
  go: (path: string, query?: Record<string, string | number | undefined>) => void;
} {
  const [h, setH] = useState(() => window.location.hash);

  useEffect(() => {
    const onChange = () => setH(window.location.hash);
    window.addEventListener('hashchange', onChange);
    return () => window.removeEventListener('hashchange', onChange);
  }, []);

  const go = useCallback(
    (path: string, query?: Record<string, string | number | undefined>) => {
      let s = `#${path}`;
      if (query) {
        const sp = new URLSearchParams();
        for (const [k, v] of Object.entries(query)) {
          if (v !== undefined && v !== '') sp.set(k, String(v));
        }
        const qs2 = sp.toString();
        if (qs2) s += `?${qs2}`;
      }
      if (window.location.hash === s) {
        setH(s);
        return;
      }
      window.location.hash = s;
      try {
        localStorage.setItem(ROUTE_KEY, s);
      } catch {
        // 隐私模式下写不了就算了，不影响导航
      }
    },
    []
  );

  const { path, query } = parseHash();
  return { path, query, go };
}

/** 首次进入时恢复上次的页面，否则落到时间轴。 */
export function restoreRoute(): string {
  try {
    const saved = localStorage.getItem(ROUTE_KEY);
    if (saved && saved.startsWith('#/')) return saved;
  } catch {
    // 忽略
  }
  return '#/timeline';
}

// ---------- 取数 ----------

/**
 * 取数小工具：负责 loading / error / 竞态。
 *
 * 竞态不能不管：翻月比取数快时，先发的请求可能后到，把新月份的数据盖掉。
 * 这里用自增序号丢弃过期响应，比 AbortController 更省事（GET 请求断掉也浪费）。
 */
export function useAsync<T>(fn: () => Promise<T>, deps: unknown[]): {
  data: T | null;
  error: string | null;
  loading: boolean;
  reload: () => void;
} {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setError(null);
    fn()
      .then((d) => {
        if (alive) {
          setData(d);
          setLoading(false);
        }
      })
      .catch((e: unknown) => {
        if (alive) {
          setError(e instanceof Error ? e.message : String(e));
          setLoading(false);
        }
      });
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, nonce]);

  return { data, error, loading, reload: () => setNonce((n) => n + 1) };
}
