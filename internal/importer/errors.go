package importer

import (
	"errors"
	"fmt"
)

// ErrBatchNotFound 是「批次号不存在」，API 层据此返回 404。
var ErrBatchNotFound = errors.New("批次不存在")

// ErrBatchHasData 是「该批次已成功导入，请先撤销再删除记录」。
var ErrBatchHasData = errors.New("该批次已成功导入，请先撤销再删除记录")

// UserError 是**说给用户听**的错误：文案本身就是中文提示，API 可以原样回给客户端。
//
// 为什么要区分：importer 的普通 error 里可能夹着文件路径、SQL 片段、内部状态，
// 直接回给客户端等于把内部结构递给调用方。带这个类型的错误才允许外传，
// 其余一律只进服务端日志。
type UserError struct{ Msg string }

func (e *UserError) Error() string { return e.Msg }

// UserErrf 构造一个可以直接回给用户的错误。
func UserErrf(format string, a ...any) error {
	return &UserError{Msg: fmt.Sprintf(format, a...)}
}
