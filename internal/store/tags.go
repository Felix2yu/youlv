package store

import (
	"context"
)

// TagDTO 是标签字典里的一条。
type TagDTO struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	// Count 是挂了这个标签的到访条数。
	Count int `json:"count"`
	// FirstVisitAt / LastVisitAt 是首末到访时刻。
	FirstVisitAt *int64 `json:"first_visit_at,omitempty"`
	LastVisitAt  *int64 `json:"last_visit_at,omitempty"`
}

// Tags 返回标签字典及各自的使用次数，按使用次数从多到少。
func (s *Store) Tags(ctx context.Context, limit, offset int) ([]TagDTO, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT t.id, t.name, COALESCE(t.color,''), COUNT(vt.visit_id),
		       MIN(v.arrived_at), MAX(v.arrived_at)
		FROM tags t
		LEFT JOIN visit_tags vt ON vt.tag_id = t.id
		LEFT JOIN visits v ON v.id = vt.visit_id
		GROUP BY t.id
		ORDER BY COUNT(vt.visit_id) DESC, t.name
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagDTO{}
	for rows.Next() {
		var t TagDTO
		if err := rows.Scan(&t.ID, &t.Name, &t.Color, &t.Count,
			&t.FirstVisitAt, &t.LastVisitAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
