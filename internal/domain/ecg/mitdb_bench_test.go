package ecg

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMITBIHBenchmark 用 PhysioNet MIT-BIH 心律失常数据库（带专家心跳标注）
// 实测 R 峰检测器的灵敏度（Se）与阳性预测值（PPV）。
//
// 这不是 CI 用例——它依赖外部数据，没有就跳过：
//
//	mitdb_dir=... go test ./internal/domain/ecg -run MITBIH -v
//
// 数据准备（一次性，见 scripts/bench-mitdb/README.md）：
//  1. 从 https://physionet.org/content/mitdb/1.0.0/ 下载若干条
//     记录的 .hea/.dat/.atr 到同一目录
//  2. python3 scripts/bench-mitdb/export.py <记录号...>
//     （需要 wfdb；产出 <rec>.sig 与 <rec>.beats）
//
// 判据采用文献惯例：±150ms 容差窗口内命中专家标注记 TP。
// 该库是 360Hz 双导联的 30 分钟长记录，基线漂移与噪声远重于
// Apple Watch 的 30 秒片段——这里的数字才是检测器的下限，
// 不是它在知常真实数据上的表现。

const mitdbTolSec = 0.150

func TestMITBIHBenchmark(t *testing.T) {
	dir := os.Getenv("mitdb_dir")
	if dir == "" {
		t.Skip("设置 mitdb_dir=<MIT-BIH 导出目录> 后运行（见脚本注释）")
	}
	sigs, err := filepath.Glob(filepath.Join(dir, "*.sig"))
	if err != nil || len(sigs) == 0 {
		t.Skipf("%s 下没有 .sig 文件，先跑 scripts/bench-mitdb/export.py", dir)
	}
	sort.Strings(sigs)

	t.Logf("%-6s %8s %5s %5s %5s %8s %8s %9s", "rec", "beats", "TP", "FP", "FN", "Se%", "PPV%", "ms")
	var totBeats, totTP, totFP, totFN int
	for _, sp := range sigs {
		rec := strings.TrimSuffix(filepath.Base(sp), ".sig")
		fs, ann := readBeatsFile(t, filepath.Join(dir, rec+".beats"))
		sig := readSigFile(t, sp)
		if fs <= 0 || len(sig) < 64 || len(ann) == 0 {
			t.Logf("%-6s 跳过（fs=%v 信号=%d 标注=%d）", rec, fs, len(sig), len(ann))
			continue
		}
		start := time.Now()
		res := DetectRPeaks(sig, fs)
		elapsed := time.Since(start)
		tol := int(mitdbTolSec * fs)
		tp, fp, fn := matchBeats(ann, res.Peaks, tol)
		se, ppv := 0.0, 0.0
		if tp+fn > 0 {
			se = float64(tp) / float64(tp+fn)
		}
		if tp+fp > 0 {
			ppv = float64(tp) / float64(tp+fp)
		}
		t.Logf("%-6s %8d %5d %5d %5d %8.2f %8.2f %9.0f",
			rec, len(ann), tp, fp, fn, se*100, ppv*100, float64(elapsed.Microseconds())/1000)
		if trace := os.Getenv("mitdb_trace"); trace == rec {
			t.Logf("  %s 的 FN（漏检）位置：%v", rec, missPositions(ann, res.Peaks, tol, fs, false))
			t.Logf("  %s 的 FP（误检）位置：%v", rec, missPositions(ann, res.Peaks, tol, fs, true))
		}
		totBeats += len(ann)
		totTP += tp
		totFP += fp
		totFN += fn
	}
	if totBeats == 0 {
		t.Fatal("一条可用的都没有")
	}
	se := float64(totTP) / float64(totTP+totFN)
	ppv := float64(totTP) / float64(totTP+totFP)
	t.Logf("合计   %8d %5d %5d %5d %8.2f %8.2f", totBeats, totTP, totFP, totFN, se*100, ppv*100)
	// 底线：合计 Se ≥ 85%、PPV ≥ 85%。这条线管的是「别烂到不能用」，
	// 不是追平论文——库里专挑的起搏/噪声/室早难例（104/108/111/119）
	// 会把合计拉到 90% 上下，当前实测数字与分记录解读见
	// docs/ecg-analysis.md §7。开发中真出过事：T 波假峰没删干净时
	// PPV 53%，删峰仲裁写错时 Se 级联崩到 18.8%，都在底线之外。
	if se < 0.85 || ppv < 0.85 {
		t.Errorf("检测质量跌破底线：Se=%.2f%% PPV=%.2f%%", se*100, ppv*100)
	}
}

// readBeatsFile 读 export.py 产出的标注文件：首行 fs=<采样率>，其后每行一个样本号。
func readBeatsFile(t *testing.T, path string) (float64, []int) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("打开标注失败: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	if !sc.Scan() {
		t.Fatalf("标注文件为空: %s", path)
	}
	fsStr := strings.TrimPrefix(strings.TrimSpace(sc.Text()), "fs=")
	fs, err := strconv.ParseFloat(fsStr, 64)
	if err != nil {
		t.Fatalf("标注首行不是 fs=: %s", sc.Text())
	}
	var out []int
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			t.Fatalf("标注行不是整数: %q", line)
		}
		out = append(out, n)
	}
	return fs, out
}

// readSigFile 读 export.py 产出的 float64 小端信号。
func readSigFile(t *testing.T, path string) []float64 {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读信号失败: %v", err)
	}
	out := make([]float64, len(raw)/8)
	for i := range out {
		out[i] = float64(binary.LittleEndian.Uint64(raw[i*8 : i*8+8]))
	}
	return out
}

// matchBeats 把检测峰与专家标注配对：每个标注找容差内最近的未占用峰。
func matchBeats(ann, peaks []int, tol int) (tp, fp, fn int) {
	used := make([]bool, len(peaks))
	for _, a := range ann {
		// peaks 升序；定位第一个 >= a 的峰，只需看它和左邻
		i := sort.SearchInts(peaks, a)
		best, bestD := -1, tol+1
		for _, j := range []int{i - 1, i} {
			if j < 0 || j >= len(peaks) || used[j] {
				continue
			}
			d := peaks[j] - a
			if d < 0 {
				d = -d
			}
			if d < bestD {
				best, bestD = j, d
			}
		}
		if best >= 0 {
			used[best] = true
			tp++
		} else {
			fn++
		}
	}
	for _, u := range used {
		if !u {
			fp++
		}
	}
	return tp, fp, fn
}

// missPositions 列出未配对的样本号（格式化成 m:ss）：
// fp=false 取漏检的标注位置，fp=true 取误检的峰位置。
// 用来回答「Se 掉了，漏在哪」——多半集中在记录里的噪声段。
func missPositions(ann, peaks []int, tol int, fs float64, fp bool) []string {
	used := make([]bool, len(peaks))
	matchedAnn := make([]bool, len(ann))
	for i, a := range ann {
		j := sort.SearchInts(peaks, a)
		best, bestD := -1, tol+1
		for _, k := range []int{j - 1, j} {
			if k < 0 || k >= len(peaks) || used[k] {
				continue
			}
			d := peaks[k] - a
			if d < 0 {
				d = -d
			}
			if d < bestD {
				best, bestD = k, d
			}
		}
		if best >= 0 {
			used[best] = true
			matchedAnn[i] = true
		}
	}
	var out []string
	if fp {
		for j, u := range used {
			if !u {
				out = append(out, mmss(peaks[j], fs))
			}
		}
		return out
	}
	for i, m := range matchedAnn {
		if !m {
			out = append(out, mmss(ann[i], fs))
		}
	}
	return out
}

// mmss 把样本号格式化成 m:ss。
func mmss(sample int, fs float64) string {
	sec := float64(sample) / fs
	return fmt.Sprintf("%d:%02d", int(sec)/60, int(sec)%60)
}
