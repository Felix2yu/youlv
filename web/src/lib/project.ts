// Web Mercator（EPSG:3857）投影。
//
// 为什么必须投影：经纬度是角度，直接当平面坐标画，高纬度地区会被横向拉长
// （哈尔滨到北京在经纬网格上看着比实际宽得多）。地图底图的瓦片也是 3857，
// 自绘层与 Leaflet 层用同一套投影才能对得上。

export interface Pt {
  x: number;
  y: number;
}

/** 经纬度 → 归一化墨卡托（0..1）。 */
export function project(lat: number, lon: number): Pt {
  const x = (lon + 180) / 360;
  const s = Math.sin((lat * Math.PI) / 180);
  const y = 0.5 - Math.log((1 + s) / (1 - s)) / (4 * Math.PI);
  return { x, y };
}

/** 归一化墨卡托 → 经纬度。 */
export function unproject(x: number, y: number): { lat: number; lon: number } {
  const lon = x * 360 - 180;
  const n = Math.PI * (1 - 2 * y);
  const lat = (180 / Math.PI) * Math.atan(Math.sinh(n));
  return { lat, lon };
}

/**
 * 视口：中心点 + 缩放倍数。
 *
 * k=1 表示整个世界宽度刚好铺满视口宽度，k=2 放大一倍。存中心而非左上角，
 * 是为了缩放时保持鼠标指向的那个点不动——只有以中心为锚，缩放前后的映射
 * 才是简单的相似变换。
 */
export interface View {
  cx: number;
  cy: number;
  k: number;
}

/** 把经纬度范围换算成刚好装下它的视口。 */
export function fitBounds(
  bounds: { south: number; west: number; north: number; east: number },
  width: number,
  height: number,
  padPx = 24
): View {
  const a = project(bounds.north, bounds.west);
  const b = project(bounds.south, bounds.east);
  const w = Math.max(1e-6, Math.abs(b.x - a.x));
  const h = Math.max(1e-6, Math.abs(b.y - a.y));
  const kx = (width - padPx * 2) / (w * width);
  const ky = (height - padPx * 2) / (h * height);
  return {
    cx: (a.x + b.x) / 2,
    cy: (a.y + b.y) / 2,
    // 上限与滚轮/按钮的缩放上限保持一致：只有一个地点时范围退化成一个点，
    // 不封顶会算出 k≈1e6（画面直接糊成一片，用户以为坏了）。
    k: Math.max(0.9, Math.min(4000, Math.min(kx, ky)))
  };
}

/** 视口 → 屏幕像素。 */
export function toScreen(p: Pt, v: View, width: number, height: number): Pt {
  return {
    x: (p.x - v.cx) * v.k * width + width / 2,
    y: (p.y - v.cy) * v.k * width + height / 2
  };
}

/** 屏幕像素 → 归一化墨卡托，用于「以鼠标为锚缩放」。 */
export function fromScreen(sx: number, sy: number, v: View, width: number, height: number): Pt {
  return {
    x: (sx - width / 2) / (v.k * width) + v.cx,
    y: (sy - height / 2) / (v.k * width) + v.cy
  };
}

/** 视口的经纬度范围，供后端 bbox 查询。留 20% 余量，拖到边缘不会空白。 */
export function viewBounds(
  v: View,
  width: number,
  height: number
): { south: number; west: number; north: number; east: number } {
  const m = 0.2;
  const halfW = width / 2 / (v.k * width);
  const halfH = height / 2 / (v.k * width);
  const a = unproject(v.cx - halfW * (1 + m), v.cy - halfH * (1 + m));
  const b = unproject(v.cx + halfW * (1 + m), v.cy + halfH * (1 + m));
  return { west: a.lon, north: a.lat, east: b.lon, south: b.lat };
}
