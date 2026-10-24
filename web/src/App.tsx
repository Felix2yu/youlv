import { useEffect, useMemo } from 'react';
import { api } from './lib/api';
import { useAsync, useRoute, useTheme } from './lib/hooks';
import type { Meta } from './lib/types';
import Timeline from './pages/Timeline';
import MapPage from './pages/MapPage';
import Places from './pages/Places';
import Stats from './pages/Stats';
import Import from './pages/Import';
import Settings from './pages/Settings';
import { Spinner } from './components/ui';

const NAV = [
  { path: '/timeline', label: '时间轴', icon: 'M4 6h16M4 12h16M4 18h10' },
  { path: '/map', label: '足迹', icon: 'M12 21s7-6.3 7-11a7 7 0 1 0-14 0c0 4.7 7 11 7 11Z' },
  { path: '/places', label: '地点', icon: 'M4 20V9l8-5 8 5v11M9 20v-6h6v6' },
  { path: '/stats', label: '统计', icon: 'M5 19V9m7 10V5m7 14v-7' },
  { path: '/import', label: '导入', icon: 'M12 16V4m0 0L8 8m4-4 4 4M4 20h16' },
  { path: '/settings', label: '设置', icon: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z' }
] as const;

export default function App() {
  const { path, go } = useRoute();
  const [theme] = useTheme();
  const meta = useAsync<Meta>(() => api.meta(), []);

  // 空库时直接把人带到导入页：一个没有任何数据的足迹应用，
  // 首屏应该是「把数据弄进来」而不是六个空页面。
  useEffect(() => {
    if (meta.data && !meta.data.has_data && path !== '/import' && path !== '/settings') {
      go('/import');
    }
  }, [meta.data, path, go]);

  const title = useMemo(() => NAV.find((n) => n.path === path)?.label ?? '时间轴', [path]);

  return (
    <div className="flex min-h-screen bg-paper text-ink">
      {/* 侧栏：桌面常驻，移动设备收成顶部一条 */}
      <aside className="sticky top-0 hidden h-screen w-40 shrink-0 flex-col border-r border-line bg-surface px-2 py-3 sm:flex">
        <div className="mb-4 flex items-center gap-2 px-2">
          <span className="grid size-7 place-items-center rounded-md bg-accent text-[13px] font-semibold text-white">
            履
          </span>
          <span className="text-[14px] font-semibold tracking-wide">游履</span>
        </div>
        <nav className="flex flex-col gap-0.5">
          {NAV.map((n) => {
            const active = path === n.path;
            return (
              <button
                key={n.path}
                onClick={() => go(n.path)}
                className={`flex items-center gap-2 rounded-md px-2 py-1.5 text-[13px] transition-colors ${
                  active
                    ? 'bg-accent-soft font-medium text-accent'
                    : 'text-muted hover:bg-raised hover:text-ink'
                }`}
              >
                <svg
                  viewBox="0 0 24 24"
                  className="size-4 shrink-0"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.8"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                >
                  <path d={n.icon} />
                </svg>
                {n.label}
              </button>
            );
          })}
        </nav>
        <div className="mt-auto px-2 text-[11px] leading-relaxed text-faint">
          {meta.data && meta.data.has_data ? (
            <>
              <div className="tnum">{meta.data.visits.toLocaleString('zh-CN')} 次到访</div>
              <div className="tnum">{meta.data.places.toLocaleString('zh-CN')} 个地点</div>
            </>
          ) : (
            <div>还没有数据</div>
          )}
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        {/* 移动端顶栏 */}
        <header className="sticky top-0 z-10 flex items-center gap-2 border-b border-line bg-surface/95 px-3 py-2 backdrop-blur sm:hidden">
          <span className="grid size-6 place-items-center rounded bg-accent text-[12px] font-semibold text-white">
            履
          </span>
          <span className="text-[13px] font-semibold">{title}</span>
          <nav className="ml-auto flex gap-0.5 overflow-x-auto">
            {NAV.map((n) => (
              <button
                key={n.path}
                onClick={() => go(n.path)}
                className={`shrink-0 rounded px-2 py-1 text-[12px] ${
                  path === n.path ? 'bg-accent-soft text-accent' : 'text-muted'
                }`}
              >
                {n.label}
              </button>
            ))}
          </nav>
        </header>

        <main className="min-w-0 flex-1 px-3 py-3 sm:px-5 sm:py-4">
          {meta.loading ? (
            <div className="py-16">
              <Spinner label="正在读取数据" />
            </div>
          ) : (
            <Router path={path} meta={meta.data} />
          )}
        </main>
      </div>
    </div>
  );
}

function Router({ path, meta }: { path: string; meta: Meta | null }) {
  switch (path) {
    case '/map':
      return <MapPage />;
    case '/places':
      return <Places />;
    case '/stats':
      return <Stats />;
    case '/import':
      return <Import meta={meta} />;
    case '/settings':
      return <Settings meta={meta} />;
    case '/timeline':
    default:
      return <Timeline />;
  }
}
