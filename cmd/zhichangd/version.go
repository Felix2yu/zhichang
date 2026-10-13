package main

// version 由 CI 注入：-ldflags "-X main.version=v1.2.3"；本地 go build 时为 "dev"。
//
// 该变量必须被真正引用（启动日志、/api/health、version 子命令），
// 否则链接器会优化掉符号，-X 静默失效。
var version = "dev"
