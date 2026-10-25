-- 底图默认值切换：CARTO → 高德。
--
-- 老版本把 CARTO 的浅色底图写成了默认值，而它在国内多地直接连不通
-- （瓦片请求超时，地图一片空白，且前端看不出是「网络不通」还是「没数据」）。
--
-- 只有在用户「从未改过底图」时才做迁移：判断依据是 map.tiles_url 仍等于
-- 老的默认值。用户自己填过别的地址，说明他有自己的图源，不能覆盖。
DELETE FROM settings
 WHERE key = 'map.frame'
   AND EXISTS (SELECT 1 FROM settings
                WHERE key = 'map.tiles_url'
                  AND value = 'https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png');

DELETE FROM settings
 WHERE key = 'map.tiles_url'
   AND value = 'https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png';

-- 子域与版权跟着默认值走，否则会残留 a,b,c,d 与 CARTO 的署名。
DELETE FROM settings
 WHERE key IN ('map.tiles_subdomains', 'map.tiles_attribution')
   AND NOT EXISTS (SELECT 1 FROM settings
                    WHERE key IN ('map.tiles_url')
                      AND value <> '');
