# MIT-BIH 基准测试

用 PhysioNet 的 MIT-BIH 心律失常数据库实测 `internal/domain/ecg` 的
R 峰检测器，回答「这个自己实现的 Pan-Tompkins 到底准不准」。

MIT-BIH 是心搏检测领域的事实标准评测集：30 分钟长的双导联 Holter
式记录，每一条都带 cardiologist 手工标注的心跳位置（`.atr` 注释），
其中专门挑了房颤、室早、二联律、起搏、噪声段等难例。一个检测器
在这上面的数字才是它的下限；知常实际处理的 Apple Watch 30 秒
单导联片段比它干净得多。

## 步骤

```bash
# 1. 下载记录（.hea/.dat/.atr 三件套）到一个目录
mkdir -p /tmp/mitdb && cd /tmp/mitdb
for r in 100 101 102 103 104 105 106 107 108 109 111 115 118 119 122 124 201; do
  for ext in hea dat atr; do
    curl -sSL -C - -o "$r.$ext" "https://physionet.org/files/mitdb/1.0.0/$r.$ext"
  done
done

# 2. 导出成 Go 测试能读的中间格式（需要 wfdb）
pip install wfdb
python3 scripts/bench-mitdb/export.py

# 3. 跑测试（数据目录用环境变量指过去）
mitdb_dir=/tmp/mitdb go test ./internal/domain/ecg -run MITBIH -v
```

`export.py` 只导出第一导联（MLII）与**心跳类**标注
（N/L/R/B/A/a/J/S/V/r/F/e/j/n/E/Q/f/），节律标记、噪声标记等
非心跳注释不参与判分——基准只问「心跳找得准不准」。

## 判据

±150ms 容差窗口内命中专家标注记 TP，文献惯例。输出每条记录的
Se（灵敏度 = TP/(TP+FN)）与 PPV（阳性预测值 = TP/(TP+FP)），
以及 30 分钟记录的检测耗时。合计 Se < 95% 或 PPV < 90% 时测试
失败——这是「别烂到不能用」的底线，不是追平论文。

## 什么时候该重跑

- 改动 `DetectRPeaks` 的任何参数（带宽、阈值系数、补检规则）之后
- 每次发版前（可选）
