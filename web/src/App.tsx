import { useEffect, useId, useMemo } from 'react';
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

// 品牌折叠地图（与 web/icon-src/ 的应用图标同一图形），侧栏与移动端顶栏共用。
// 走 mask 镂空：折痕、旅程、定位针处透出 span 的主题色底，明暗主题都成立。
function BrandMark({ className }: { className?: string }) {
  const maskId = useId();
  return (
    <svg viewBox="0 0 24 24" className={className} aria-hidden="true">
      <mask id={maskId}>
        <g transform="translate(-4.38,-3.36) scale(0.06)">
          <path
            d="M108,152 L218,120 L328,152 L438,120 L438,360 L328,392 L218,360 L108,392 Z"
            fill="#fff"
          />
          <path
            d="M218,120 L218,360 M328,152 L328,392"
            stroke="#000"
            strokeWidth="16"
            fill="none"
          />
          <path
            d="M158,346 C190,300 178,268 240,250 C296,234 330,232 372,210"
            fill="none"
            stroke="#000"
            strokeWidth="15"
            strokeLinecap="round"
            strokeDasharray="0.1 26"
          />
          <path
            d="M372,150 c-17,0 -30,13 -30,30 c0,22 30,48 30,48 c0,0 30,-26 30,-48 c0,-17 -13,-30 -30,-30 Z"
            fill="#000"
          />
          <circle cx="372" cy="179" r="12" fill="#fff" />
        </g>
      </mask>
      <rect width="24" height="24" fill="currentColor" mask={`url(#${maskId})`} />
    </svg>
  );
}

export default function App() {
  const { path, go } = useRoute();
  useTheme();
  const meta = useAsync<Meta>(() => api.meta(), []);

  // 空库时直接把人带到导入页：一个没有任何数据的足迹应用，
  // 首屏应该是「把数据弄进来」而不是六个空页面。
  //
  // 这里用「渲染期派生」而不是 effect 里改导航：目标是路径在渲染期就能算出来，
  // 界面立刻切到导入页；effect 只负责把地址栏同步过去（写 URL 是外部系统
  // 同步，不是 React 状态变更）。effect 里调 go() 属于「副作用改状态」，
  // 会多渲染一轮，且在 StrictMode 下会跑两次。
  const wantsImport = meta.data != null && !meta.data.has_data;
  const targetPath = wantsImport && path !== '/import' && path !== '/settings' ? '/import' : path;

  useEffect(() => {
    if (targetPath !== path) window.location.hash = '#/import';
  }, [targetPath, path]);

  const title = useMemo(
    () => NAV.find((n) => n.path === targetPath)?.label ?? '时间轴',
    [targetPath]
  );

  return (
    <div className="flex min-h-screen bg-paper text-ink">
      {/* 侧栏：桌面常驻，移动设备收成顶部一条 */}
      <aside className="sticky top-0 hidden h-screen w-40 shrink-0 flex-col border-r border-line bg-surface px-2 py-3 sm:flex">
        <div className="mb-4 flex items-center gap-2 px-2">
          <span className="grid size-7 place-items-center rounded-md bg-accent text-white">
            <BrandMark className="size-4" />
          </span>
          <span className="text-[14px] font-semibold tracking-wide">游履</span>
        </div>
        <nav className="flex flex-col gap-0.5">
          {NAV.map((n) => {
            const active = targetPath === n.path;
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
          <span className="grid size-6 place-items-center rounded bg-accent text-white">
            <BrandMark className="size-3.5" />
          </span>
          <span className="text-[13px] font-semibold">{title}</span>
          <nav className="ml-auto flex gap-0.5 overflow-x-auto">
            {NAV.map((n) => (
              <button
                key={n.path}
                onClick={() => go(n.path)}
                className={`shrink-0 rounded px-2 py-1 text-[12px] ${
                  targetPath === n.path ? 'bg-accent-soft text-accent' : 'text-muted'
                }`}
              >
                {n.label}
              </button>
            ))}
          </nav>
        </header>

        {/* 取不到 meta 时必须有话说：否则侧栏只剩「还没有数据」，
            用户会以为库是空的，而不是令牌没填对。 */}
        {meta.error ? (
          <div className="flex items-center gap-2 border-b border-warn/40 bg-warn-soft px-3 py-1.5 text-[12px] text-warn">
            <span className="min-w-0 flex-1 truncate">{meta.error}</span>
            <button onClick={meta.reload} className="shrink-0 underline">
              重试
            </button>
          </div>
        ) : null}

        <main className="min-w-0 flex-1 px-3 py-3 sm:px-5 sm:py-4">
          {meta.loading ? (
            <div className="py-16">
              <Spinner label="正在读取数据" />
            </div>
          ) : (
            <Router path={targetPath} meta={meta.data} metaReload={meta.reload} />
          )}
        </main>
      </div>
    </div>
  );
}

function Router({
  path,
  meta,
  metaReload
}: {
  path: string;
  meta: Meta | null;
  metaReload: () => void;
}) {
  switch (path) {
    case '/map':
      return <MapPage />;
    case '/places':
      return <Places />;
    case '/stats':
      return <Stats />;
    case '/import':
      // 导入写完/撤销完必须刷新侧栏计数：不刷新的话结果页写着「写入 N 条」，
      // 回到时间轴侧栏还是旧数字，用户会怀疑到底导没导进去。
      return <Import meta={meta} onDataChanged={metaReload} />;
    case '/settings':
      return <Settings meta={meta} onDataChanged={metaReload} />;
    case '/timeline':
    default:
      return <Timeline onDataChanged={metaReload} />;
  }
}
