package main

// version 由 CI 注入：-ldflags "-X main.version=v1.2.3"；本地 go build 时为 "dev"。
//
// 这个变量必须保持被真正引用（main.go 的启动日志与 -version 都用了它），
// 否则链接器会把符号优化掉，-X 注入静默失效。
var version = "dev"
