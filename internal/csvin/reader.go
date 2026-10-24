// Package csvin 负责把 Rond 导出的 CSV 读成规范化记录，并识别文件类型。
//
// 所有容错都集中在 Decode 里：BOM、字段数不齐、裸引号、非法 UTF-8。
// 上层一律看不到这些脏活，只拿到干净的 [][]string。
package csvin

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Kind 是四种源文件的类型标识。
type Kind string

const (
	KindPlaces  Kind = "places"
	KindVisits  Kind = "visits"
	KindTrips   Kind = "trips"
	KindWeather Kind = "weather"
)

// Label 返回中文名，用于导入报告与前端展示。
func (k Kind) Label() string {
	switch k {
	case KindPlaces:
		return "地点记录"
	case KindVisits:
		return "到访记录"
	case KindTrips:
		return "交通记录"
	case KindWeather:
		return "天气数据"
	}
	return string(k)
}

// File 是一个已解码的源文件。
type File struct {
	Kind    Kind
	Name    string
	Size    int64
	Sha256  string
	Header  []string   // 原始表头（未归一化，用于回显）
	Records [][]string // 数据行，已按表头宽度补齐/截断
	LineOf  []int      // Records[i] 在原始文件中的物理行号（1-based，含表头）
}

// Issue 是解码/校验阶段发现的问题。
//
// 每个字段都要带 json tag：这个结构直接作为 /api/imports/{id}/issues 的
// 响应体返回给前端，缺 tag 会输出 Go 风格的大写字段名。
type Issue struct {
	// Kind 是问题所属的文件类型（places|visits|trips|weather）。
	//
	// 它不在 csvin 包内产生——解码阶段还不知道文件是哪种类型，
	// 是 importer 在识别出类型后回填的。落库时写进 import_issues.kind，
	// 让问题清单能按文件维度过滤（否则这个维度在落库时就丢了）。
	Kind     string `json:"kind,omitempty"`
	Line     int    `json:"line"`
	RowNo    int    `json:"row_no"`
	Column   string `json:"column"`
	Field    string `json:"field"`
	Value    string `json:"value"`
	Severity string `json:"severity"` // error|warn|info
	Code     string `json:"code"`
	Message  string `json:"message"`
	Hint     string `json:"hint"`
}

const (
	SevError = "error"
	SevWarn  = "warn"
	SevInfo  = "info"
)

var (
	ErrEmptyFile = errors.New("文件为空")
	ErrNoHeader  = errors.New("文件没有表头")
)

// Decode 读取一个 CSV 文件。它不判断类型（那是 Detect 的事），
// 只负责把字节变成规范化的行，并为每行记录真实物理行号。
func Decode(name string, r io.Reader) (*File, []Issue, error) {
	br := bufio.NewReader(r)

	// 剥掉 UTF-8 BOM。Rond 导出的四份文件都带 BOM，不剥掉第一个列名会
	// 变成 "\ufeff日期"，导致按名寻址全线失效。
	if bom, err := br.Peek(3); err == nil && len(bom) == 3 &&
		bom[0] == 0xEF && bom[1] == 0xBB && bom[2] == 0xBF {
		_, _ = br.Discard(3)
	}

	h := sha256.New()
	var buf bytes.Buffer
	rd := csv.NewReader(io.TeeReader(br, io.MultiWriter(h, &buf)))
	rd.FieldsPerRecord = -1 // 字段数不齐不报错，交给下面的补齐逻辑
	rd.LazyQuotes = true    // 中文文本里可能有裸引号
	rd.TrimLeadingSpace = true

	header, err := rd.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil, ErrEmptyFile
		}
		return nil, nil, fmt.Errorf("解析 CSV 表头失败: %w", err)
	}
	if len(bytes.TrimSpace(buf.Bytes())) == 0 {
		return nil, nil, ErrEmptyFile
	}

	f := &File{Name: name, Header: sanitizeAll(header)}
	width := len(f.Header)
	if width == 0 {
		return nil, nil, ErrNoHeader
	}

	var issues []Issue
	rowNo := 0
	for {
		row, err := rd.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// 单行解析失败不应中断整批：记 error，跳到下一行。
			rowNo++
			issues = append(issues, Issue{
				RowNo: rowNo, Line: rowNo + 1, Severity: SevError,
				Code: "ERR_CSV_ROW", Message: fmt.Sprintf("该行无法解析：%v", err),
				Hint: "检查是否有未闭合的引号或换行",
			})
			continue
		}
		rowNo++
		line, _ := rd.FieldPos(0) // 上一 Read 的第 0 字段所在行，1-based
		if line <= 0 {
			line = rowNo + 1 // 退化：无多行引号时表头占 1 行
		}
		f.Records = append(f.Records, sanitizeAll(fitRow(row, width, &issues, rowNo, line)))
		f.LineOf = append(f.LineOf, line)
	}

	f.Size = int64(buf.Len())
	f.Sha256 = fmt.Sprintf("%x", h.Sum(nil))
	return f, issues, nil
}

// fitRow 把一行补齐或截断到 width，并为此产生 warn。
func fitRow(row []string, width int, issues *[]Issue, rowNo, line int) []string {
	if len(row) == width {
		return row
	}
	if len(row) < width {
		*issues = append(*issues, Issue{
			RowNo: rowNo, Line: line, Severity: SevWarn, Code: "WARN_ROW_TOO_SHORT",
			Message: fmt.Sprintf("该行只有 %d 列，表头有 %d 列，缺的列按空值处理", len(row), width),
			Hint:    "通常无害；若整列都缺，说明导出时少勾了字段",
		})
		out := make([]string, width)
		copy(out, row)
		return out
	}
	*issues = append(*issues, Issue{
		RowNo: rowNo, Line: line, Severity: SevWarn, Code: "WARN_ROW_TOO_LONG",
		Message: fmt.Sprintf("该行有 %d 列，表头只有 %d 列，多出的 %d 列被忽略", len(row), width, len(row)-width),
		Hint:    "检查是否有字段内容里含未转义的逗号",
	})
	return row[:width]
}

func sanitizeAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = sanitize(s)
	}
	return out
}

// sanitize 修掉非法 UTF-8，并做 NFKC 归一（全角转半角靠它）。
// 表头与字段值都走这里，保证入库字符串一致。
func sanitize(s string) string {
	if utf8.ValidString(s) {
		return norm.NFKC.String(s)
	}
	var b strings.Builder
	for _, r := range s {
		if r == utf8.RuneError {
			b.WriteRune('�')
			continue
		}
		b.WriteRune(r)
	}
	return norm.NFKC.String(b.String())
}
