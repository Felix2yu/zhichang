package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/testdb"
)

// TestMCPServesInitializeOverStdio 走一遍真实的 stdio 通道：
// 从 os.Stdin 喂一条 JSON-RPC initialize，从 os.Stdout 收回应，
// 关 stdin 之后命令应当干净退出。
func TestMCPServesInitializeOverStdio(t *testing.T) {
	dsn := testdb.DSN(t)
	setOpsEnv(t, dsn)
	pool(t) // 库不可用时先跳过，别把阻塞在 stdin 上的命令拖成测试超时

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatalf("建 stdin 管道失败: %v", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("建 stdout 管道失败: %v", err)
	}
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	t.Cleanup(func() { os.Stdin, os.Stdout = oldIn, oldOut })

	done := make(chan error, 1)
	go func() { done <- MCP(nil) }()

	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"zhichang-cli-test","version":"0"},"capabilities":{}}}`
	if _, err := fmt.Fprintln(inW, initialize); err != nil {
		t.Fatalf("写入 stdin 失败: %v", err)
	}

	// 读回应；os.File 的读超时能用在管道上，避免测试永久挂住。
	br := bufio.NewReader(outR)
	reply := readUntil(t, br, outR, `"id":1`)
	// 断言回应本身：stdout 只走 JSON-RPC，说明「日志走 stderr」这条口径没被破坏。
	if !strings.HasPrefix(strings.TrimSpace(reply), `{"jsonrpc":"2.0"`) {
		t.Errorf("stdout 上应当只有 JSON-RPC 报文，实际: %q", reply)
	}
	if !strings.Contains(reply, `"serverInfo"`) {
		t.Errorf("initialize 没有回上 serverInfo: %s", reply)
	}

	// 再走一次 tools/list：能列出来才说明 withDBAndMetrics 真的把库和指标字典接上了服务。
	if _, err := fmt.Fprintln(inW, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); err != nil {
		t.Fatalf("发送 initialized 通知失败: %v", err)
	}
	if _, err := fmt.Fprintln(inW, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`); err != nil {
		t.Fatalf("发送 tools/list 失败: %v", err)
	}
	tools := readUntil(t, br, outR, `"id":2`)
	if !strings.Contains(tools, `"tools"`) || strings.Contains(tools, `"tools":[]`) {
		t.Errorf("stdio 通道应当能列出可用的工具，实际: %s", tools)
	}

	_ = inW.Close()
	go func() { _, _ = io.Copy(io.Discard, outR) }() // 排空管道，别让服务端写阻塞

	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("stdin 关闭后 MCP 没有退出")
	}
	if runErr != nil {
		t.Errorf("stdio 会话正常结束（EOF）时 MCP 应返回 nil: %v", runErr)
	}
}

// TestMCPRejectsUnknownFlag 未定义的参数要直接报错，不能让本地客户端静默跑起来。
func TestMCPRejectsUnknownFlag(t *testing.T) {
	setOpsEnv(t, testdb.DSN(t))
	if err := MCP([]string{"-weird-flag"}); err == nil {
		t.Fatal("未知参数应当报错")
	} else if !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("错误应指向具体参数问题: %v", err)
	}
}

// TestMCPFailsOnBadConfig 配置非法时立刻报错，不拖到建立会话之后。
func TestMCPFailsOnBadConfig(t *testing.T) {
	t.Setenv("ZHICHANG_DB_DSN", testdb.DSN(t))
	t.Setenv("ZHICHANG_INGEST_TOKEN", "")
	t.Setenv("ZHICHANG_HOME_TZ", "Mars/Olympus_Mons")
	_, err := capturedRun(t, func() error { return MCP(nil) })
	if err == nil {
		t.Fatal("非法时区下 MCP 竟然启动成功")
	}
	if !strings.Contains(err.Error(), "ZHICHANG_HOME_TZ 非法") {
		t.Errorf("错误应指向时区配置: %v", err)
	}
}

// TestMCPFailsWhenDBDown 库连不上时必须报错，而不是打开一个空壳服务。
func TestMCPFailsWhenDBDown(t *testing.T) {
	t.Setenv("ZHICHANG_DB_DSN", "postgres://postgres:postgres@127.0.0.1:1/zhichang?sslmode=disable")
	t.Setenv("ZHICHANG_INGEST_TOKEN", "")
	_, err := capturedRun(t, func() error {
		return MCP([]string{"-dsn", "postgres://postgres:postgres@127.0.0.1:1/zhichang?sslmode=disable"})
	})
	if err == nil {
		t.Fatal("库连不上时 MCP 应当报错")
	}
	if !strings.Contains(err.Error(), "连接数据库失败") {
		t.Errorf("错误应说清是数据库连不上: %v", err)
	}
}

// readUntil 逐行读到包含 want 的那一行为止（跳过中间可能夹着的其它报文），
// 每次读都重新设一个绝对超时，这样 stdin/stdout 管道不会把测试永久挂住。
func readUntil(t *testing.T, br *bufio.Reader, f *os.File, want string) string {
	t.Helper()
	for {
		if err := f.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
			t.Fatalf("设置读超时失败: %v", err)
		}
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("等不到包含 %s 的回应: %v（最后读到: %s）", want, err, line)
		}
		if strings.Contains(line, want) {
			return line
		}
	}
}
