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

function parseHash(hash: string): { path: string; query: URLSearchParams } {
  const raw = hash.replace(/^#/, '');
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
  // hash 是路由的唯一真源：状态与地址栏不允许各存一份，
  // 否则「改了状态但没改地址栏」会出现刷新后跳回原页。
  const [h, setH] = useState(() => window.location.hash);

  useEffect(() => {
    const onChange = () => setH(window.location.hash);
    window.addEventListener('hashchange', onChange);
    return () => window.removeEventListener('hashchange', onChange);
  }, []);

  const go = useCallback((path: string, query?: Record<string, string | number | undefined>) => {
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
      // 目标就是当前页：hashchange 不会触发，手动同步一次。
      setH(s);
      return;
    }
    window.location.hash = s;
    try {
      localStorage.setItem(ROUTE_KEY, s);
    } catch {
      // 隐私模式下写不了就算了，不影响导航
    }
  }, []);

  const { path, query } = parseHash(h);
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
 * 这里用 alive 标志丢弃过期响应，比 AbortController 更省事（GET 请求断掉也浪费）。
 *
 * loading 是**派生值**而不是 effect 里 set 出来的状态：把 deps 序列化成 key，
 * 「当前 key 还没有结果」即「正在加载」。这样依赖一变化 loading 立刻为真，
 * 不必等 effect 跑完才亮；reload 时也会亮（老写法在 reload 时不亮，是个 bug）。
 * 副作用只在 promise 回调里 setState，effect 体内不做同步 setState。
 *
 * deps 必须是 JSON 稳定的原始值（字符串/数字/布尔）。现有调用点都满足；
 * 将来要传对象，请用 useMemo 稳定引用后再传 id 之类的标量。
 */
export function useAsync<T>(
  fn: () => Promise<T>,
  deps: unknown[]
): {
  data: T | null;
  error: string | null;
  loading: boolean;
  reload: () => void;
} {
  const key = JSON.stringify(deps);
  const [state, setState] = useState<{ key: string; data: T | null; error: string | null }>({
    key: '',
    data: null,
    error: null
  });
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    let alive = true;
    fn()
      .then((d) => {
        if (alive) setState({ key, data: d, error: null });
      })
      .catch((e: unknown) => {
        if (alive) {
          setState({ key, data: null, error: e instanceof Error ? e.message : String(e) });
        }
      });
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, nonce]);

  const loading = state.key !== key;

  return {
    data: state.data,
    // 旧一轮的错误不该配上新一轮的 loading。
    error: state.key === key ? state.error : null,
    loading,
    reload: () => setNonce((n) => n + 1)
  };
}
