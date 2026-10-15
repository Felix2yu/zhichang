package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Felix2yu/zhichang/internal/config"
	"github.com/Felix2yu/zhichang/internal/gpx"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// ImportRoutes 把 HAE 导出的 GPX 轨迹从本机目录导入本地库。
//
// 只有这一条路：轨迹是逐点位置数据，接入端点故意不收——把行踪上传到服务器
// 比上传心率更严重，而 .gpx 本来就在本机的导出目录里。
//
// 幂等：按文件内容指纹判定，同指纹重放不产生第二次写入，所以可以放心反复跑。
// 匹配靠「运动名 + 文件名里的本地钟点」对上 workouts 的起点；对不上或一对多的
// 都只报告不猜——猜错会把一条轨迹挂到另一天的某次运动上，界面上完全看不出来。
func ImportRoutes(args []string) error {
	fs := flag.NewFlagSet("import-routes", flag.ContinueOnError)
	dsn := fs.String("dsn", "", "覆盖 ZHICHANG_DB_DSN")
	dryRun := fs.Bool("dry-run", false, "只解析与匹配，不写库")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := fs.Arg(0)
	if dir == "" {
		return errors.New("用法: zhichangd import-routes [-dsn <DSN>] [-dry-run] <GPX 目录>")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.gpx"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("%s 下没有 .gpx 文件", dir)
	}
	sort.Strings(files)

	if *dsn != "" {
		_ = os.Setenv("ZHICHANG_DB_DSN", *dsn)
	}
	cfg, err := config.LoadForCLI()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	db, err := postgres.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	applied, err := db.Migrate(ctx)
	if err != nil {
		return err
	}
	for _, a := range applied {
		fmt.Println("已应用:", a)
	}

	var rep routeReport
	for _, path := range files {
		name, wall, ok := gpx.ParseFileName(path)
		if !ok {
			rep.noteBadName(filepath.Base(path))
			continue
		}
		r, err := gpx.ParseFile(path)
		if err != nil {
			rep.noteFailed(name, err)
			continue
		}
		sum, err := fileSHA(path)
		if err != nil {
			rep.noteFailed(name, err)
			continue
		}
		ids, err := db.MatchWorkoutByWall(ctx, name, wall)
		if err != nil {
			return err
		}
		switch {
		case len(ids) == 0:
			rep.noteUnmatched(r, name)
		case len(ids) > 1:
			rep.noteAmbiguous(r, name, len(ids))
		default:
			if *dryRun {
				rep.wouldWrite += r.Count()
				rep.files++
				break
			}
			old, err := db.RouteFingerprint(ctx, ids[0])
			if err != nil {
				return err
			}
			if old == sum {
				rep.unchanged++
				break
			}
			wrote, err := db.WriteRoute(ctx, ids[0], r, sum)
			if err != nil {
				return err
			}
			if wrote {
				rep.written++
				rep.points += r.Count()
			} else {
				rep.unchanged++
			}
		}
	}
	rep.print(len(files), *dryRun)
	if rep.written == 0 && rep.unchanged == 0 && rep.files == 0 {
		return errors.New("没有任何轨迹可导入（见上面的报告）")
	}
	return nil
}

type routeReport struct {
	written, unchanged, files, points, wouldWrite int
	unmatched                                     []string
	ambiguous                                     []string
	badName                                       []string
	failed                                        []string
}

func (r *routeReport) noteBadName(f string) {
	r.badName = append(r.badName, f)
}

func (r *routeReport) noteFailed(name string, err error) {
	r.failed = append(r.failed, fmt.Sprintf("%s: %v", name, err))
}

func (r *routeReport) noteUnmatched(rt *gpx.Route, name string) {
	r.unmatched = append(r.unmatched, fmt.Sprintf("%s（%s 无同时刻的运动记录）", rt.FileName, name))
}

func (r *routeReport) noteAmbiguous(rt *gpx.Route, name string, n int) {
	r.ambiguous = append(r.ambiguous, fmt.Sprintf("%s（%s 匹配到 %d 条）", rt.FileName, name, n))
}

func (r *routeReport) print(total int, dryRun bool) {
	if dryRun {
		fmt.Printf("解析 %d 个文件，可写入 %d 条轨迹 / %d 个点（--dry-run 未写库）\n",
			total, r.files, r.wouldWrite)
	} else {
		fmt.Printf("解析 %d 个文件，写入 %d 条轨迹 / %d 个点，内容未变 %d 条\n",
			total, r.written, r.points, r.unchanged)
	}
	for _, label := range []struct {
		name string
		list []string
	}{
		{"未匹配", r.unmatched}, {"歧义", r.ambiguous},
		{"文件名不合规则", r.badName}, {"解析失败", r.failed},
	} {
		if len(label.list) == 0 {
			continue
		}
		fmt.Printf("%s %d 个:\n", label.name, len(label.list))
		for _, x := range label.list {
			fmt.Println("  -", x)
		}
	}
}

func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
