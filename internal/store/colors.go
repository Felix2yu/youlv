package store

import "context"

// DefaultActivityColor 是活动的兜底灰，与 migrations 里 activities.color 的
// DEFAULT 保持字面一致——回填判定靠这个字面值。
const DefaultActivityColor = "#8E8E93"

// activityPalette 是活动分类的循环调色板。
//
// 选色约束：① 相邻色相拉开距离，17 个活动在统计图上不至于糊成一片；
// ② 全部是中低饱和度——浅色主题下不发刺眼，深色主题下不发闷；
// ③ 避开红绿两侧的纯色，留出「警示/成功」语义给 UI 状态标签。
var activityPalette = [...]string{
	"#2f6b4f", // 墨绿
	"#b0623a", // 赭石
	"#3d6d9e", // 靛蓝
	"#8a6ba8", // 紫藤
	"#a3452f", // 砖红
	"#2e7d7b", // 孔雀青
	"#96700f", // 芥末
	"#5b5f97", // 蓝紫
	"#4f7d2f", // 草绿
	"#a85751", // 玫瑰
	"#3f7688", // 灰蓝
	"#946b2d", // 土黄
	"#7c4d8a", // 洋紫
	"#556b2f", // 橄榄
	"#8c5a3c", // 棕褐
	"#666f85", // 蓝灰
}

// ActivityColor 按序号循环取调色板色。sort_order 即序号，同一活动
// 多次导入得到的颜色稳定不变（幂等的另一半：颜色也要幂等）。
func ActivityColor(i int) string {
	return activityPalette[i%len(activityPalette)]
}

// EnsureActivityColors 给仍是兜底灰的活动按 sort_order 上色。
//
// 为什么要回填：调色板分配是 Commit 时做的，但历史上已导入的批次
// （schema v1 早期）没有这一步。启动时跑一次，把存量补齐；
// 已有自定义颜色的活动不动。返回补色的条数。
func (s *Store) EnsureActivityColors(ctx context.Context) (int, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id FROM activities WHERE color = ? ORDER BY sort_order, name`,
		DefaultActivityColor)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	for i, id := range ids {
		if _, err := s.DB.ExecContext(ctx,
			`UPDATE activities SET color=? WHERE id=?`, ActivityColor(i), id); err != nil {
			return i, err
		}
	}
	return len(ids), nil
}
