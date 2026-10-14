// Package testdb 为集成测试自举一个 PostgreSQL 实例。
//
// 为什么不连外部库：这个仓库的仓储层（store/postgres）以及 api、mcp、pipeline、cli
// 全都直接持有 *postgres.DB，全仓约 76% 的语句必须先有一个真的库才测得到。
// 开发机与 CI 都不保证有 docker，所以这里用 embedded-postgres 在测试进程里拉起一个
// 真的 PostgreSQL 18（首次运行下载二进制到用户缓存目录，之后离线可跑）。
//
// 与生产的口径差异（必须知道，别把它当 TimescaleDB 用）：
//   - samples 在这里是普通表，不是 hypertable —— 0001 里 CREATE EXTENSION timescaledb
//     和 SELECT create_hypertable(...) 两条语句被跳过了。
//   - 因此「按 start_time 做 chunk 裁剪」这类分区行为不在覆盖范围内。
//     查询侧不依赖任何 TimescaleDB 专有函数（全仓没有 time_bucket），
//     所以 SQL 语义与生产一致；要覆盖 hypertable 本身，把 ZHICHANG_TEST_DSN 指向
//     一个真的 TimescaleDB 即可，这些测试会原样跑在那个库上。
package testdb

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Felix2yu/zhichang/migrations"
)

// 外部库优先：CI 可以指向 timescale 官方镜像，此时完全不走自举路径。
const dsnEnv = "ZHICHANG_TEST_DSN"

var (
	once    sync.Once
	started struct {
		dsn string
		err error
	}
)

// Main 起停测试库，供每个使用数据库的测试包在 TestMain 里调用：
//
//	func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }
//
// 每个测试包是独立的进程，所以各自起一个实例、各占一个端口，互不干扰。
func Main(m *testing.M) int {
	if os.Getenv(dsnEnv) != "" {
		return m.Run()
	}
	if err := start(); err != nil {
		fmt.Fprintf(os.Stderr, "testdb: 启动测试库失败: %v\n", err)
		return 1
	}
	defer func() {
		if pg != nil {
			_ = pg.Stop()
		}
		// Stop() 只清它自己管的 runtime/data 目录，父目录（含重试时的几个）得这里收。
		// 一套 PG 实例约 200MB，不回收的话跑几轮就把磁盘占满了。
		if runDir != "" {
			_ = os.RemoveAll(runDir)
		}
	}()
	return m.Run()
}

// DSN 返回一个已完成建表的测试库连接串。库起不来时跳过当前测试，
// 而不是让整条流水线红——集成测试的价值在于跑通口径，不在于阻塞构建。
func DSN(t *testing.T) string {
	t.Helper()
	if dsn := strings.TrimSpace(os.Getenv(dsnEnv)); dsn != "" {
		return dsn
	}
	if err := start(); err != nil {
		t.Skipf("测试库不可用: %v", err)
	}
	if started.err != nil {
		t.Skipf("测试库不可用: %v", started.err)
	}
	return started.dsn
}

var pg *embeddedpostgres.EmbeddedPostgres

// runDir 是本进程的测试库工作目录，由 Main 在退出前删除。
var runDir string

// start 在本进程内起一次库并建好 schema。
func start() error {
	once.Do(func() {
		dsn, p, err := boot()
		if err != nil {
			started.err = err
			return
		}
		pg, started.dsn = p, dsn
	})
	return started.err
}

func boot() (string, *embeddedpostgres.EmbeddedPostgres, error) {
	port, err := freePort()
	if err != nil {
		return "", nil, err
	}
	cache, err := cacheDir()
	if err != nil {
		return "", nil, err
	}
	// 运行目录必须每个测试进程一份：embedded-postgres 在 Stop() 里会 RemoveAll 它，
	// 而 go test 是并行跑各个包的——共用目录时先结束的那个会把还在跑的库的文件删掉。
	// 只有下载下来的发行包留在共享 cache 里。
	run, err := os.MkdirTemp("", "zhichang-testdb-")
	if err != nil {
		return "", nil, err
	}
	runDir = run

	// 首次运行要从 Maven 仓库拉发行包，两个测试进程同时判断「本地没有」而一起下载时
	// 会互相踩，所以失败后换一个运行目录重试；发行包一旦落地，后续启动就不会再遇到。
	newDB := func(sfx string, port uint32) *embeddedpostgres.EmbeddedPostgres {
		return embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
			Version(embeddedpostgres.V18).
			Port(port).
			Username("postgres").
			Password("postgres").
			Database("zhichang_test").
			CachePath(cache).
			RuntimePath(filepath.Join(run, "extracted"+sfx)).
			DataPath(filepath.Join(run, "pgdata"+sfx)).
			Logger(io.Discard).
			StartTimeout(90 * time.Second))
	}
	var p *embeddedpostgres.EmbeddedPostgres
	var lastErr error
	usedPort := port
	for attempt := range 3 {
		// 重试主要是因为端口：freePort 拿到端口到库真正 listen 之间有个窗口，
		// 多个测试包同时起库时可能撞车（go test 是并行跑各个包的）。
		sfx := ""
		if attempt > 0 {
			sfx = fmt.Sprintf("-%d", attempt)
			if usedPort, err = freePort(); err != nil {
				return "", nil, err
			}
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		p = newDB(sfx, uint32(usedPort))
		// 冷缓存时这里会下载发行包，下载竞争由锁挡住。
		if lastErr = withCacheLock(cache, p.Start); lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		_ = os.RemoveAll(run)
		return "", nil, fmt.Errorf("%w（运行目录 %s）", lastErr, run)
	}

	dsn := fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/zhichang_test?sslmode=disable", usedPort)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := applySchema(ctx, dsn); err != nil {
		_ = p.Stop()
		_ = os.RemoveAll(run)
		return "", nil, err
	}
	return dsn, p, nil
}

// applySchema 建 0001 与 0002，并把它们登记进 schema_migrations。
//
// 0003 故意留着不建：让仓储层自己的 db.Migrate() 去补，
// 这样 execScript 这条真实生产路径仍然有覆盖。
func applySchema(ctx context.Context, dsn string) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("创建连接池失败: %w", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}

	files, err := migrations.Files()
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.Version == "0003_goals.sql" {
			continue
		}
		if err := execScript(ctx, pool, deTimescale(f.SQL)); err != nil {
			return fmt.Errorf("建表 %s 失败: %w", f.Version, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`, f.Version); err != nil {
			return err
		}
	}
	return nil
}

// deTimescale 摘掉 0001 里仅有的两条 TimescaleDB 专有语句。
// 这里是精确匹配而不是模糊正则：真出现第三条同类语句时宁可让它报错，
// 也不要被一条宽松规则静默吃掉，把没测到的东西伪装成测过了。
func deTimescale(sql string) string {
	out := sql
	for _, stmt := range []string{
		"CREATE EXTENSION IF NOT EXISTS timescaledb;",
		"SELECT create_hypertable('samples', by_range('start_time', INTERVAL '1 month'), if_not_exists => true, migrate_data => true);",
	} {
		out = strings.ReplaceAll(out, stmt, "")
	}
	return out
}

// execScript 用简单协议执行整份脚本，包在单事务内（与 store/postgres 的做法一致）。
func execScript(ctx context.Context, pool *pgxpool.Pool, script string) error {
	c, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer c.Release()

	pc := c.Conn().PgConn()
	if _, err := pc.Exec(ctx, "BEGIN").ReadAll(); err != nil {
		return err
	}
	if _, err := pc.Exec(ctx, script).ReadAll(); err != nil {
		_, _ = pc.Exec(ctx, "ROLLBACK").ReadAll()
		return err
	}
	_, err = pc.Exec(ctx, "COMMIT").ReadAll()
	return err
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// cacheDir 存放下载下来的 PG 发行包，跨运行复用，所以第二次起不用联网。
func cacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "zhichang", "testdb-cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// withCacheLock 在共享缓存还是冷的时候把启动串行化。
//
// 发行包下载进的是共享 cachePath，而 embedded-postgres 的写法是「先判断文件在不在、
// 不在就写」：go test 并行跑各个包的测试二进制，多个进程会同时判定「不在」并一起写
// 同一个文件，读到半截文件的进程解不开压缩包（CI 的冷 runner 上几乎必然撞上）。
// 只有首次下载需要排队，所以用双重检查——就绪标记已在时直接并行放行。
func withCacheLock(cache string, fn func() error) error {
	ready := filepath.Join(cache, ".ready")
	if exists(ready) {
		return fn()
	}
	unlock, err := acquireLock(cache)
	if err != nil {
		return err
	}
	defer unlock()
	if exists(ready) { // 等锁的这段时间里别人已经下载好了
		return fn()
	}
	if err := fn(); err != nil {
		return err
	}
	_ = os.WriteFile(ready, []byte("ok\n"), 0o644)
	return nil
}

// acquireLock 用 mkdir 的原子性做跨进程锁。锁目录超过 10 分钟就当作残留强行取走：
// 进程被 kill -9 时来不及释放，不能让它把之后每次测试都卡死。
func acquireLock(cache string) (func(), error) {
	lock := filepath.Join(cache, ".lock")
	deadline := time.Now().Add(8 * time.Minute)
	for {
		if err := os.Mkdir(lock, 0o755); err == nil {
			return func() { _ = os.Remove(lock) }, nil
		} else if !os.IsExist(err) {
			return nil, err
		}
		if fi, err := os.Stat(lock); err == nil && time.Since(fi.ModTime()) > 10*time.Minute {
			_ = os.RemoveAll(lock)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("等待测试库缓存锁超时: %s", lock)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
