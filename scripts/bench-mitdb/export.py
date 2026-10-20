#!/usr/bin/env python3
"""把 PhysioNet MIT-BIH 记录导出成 Go 基准测试能读的中间格式。

每个记录产出两个文件：
  <rec>.sig    float64 小端、单导联（MLII）的物理单位（mV）信号
  <rec>.beats  第一行 fs=<采样率>，之后每行一个专家标注的心跳样本号

只有「心跳」类标注会被导出（N/L/R/B/A/a/J/S/V/r/F/e/j/n/E/Q/f/），
节律标记、噪声段标记等非心跳注释一律剔除——基准只问检测器
「心跳找得准不准」，不问它认不认得出这段是房颤。

前置：pip install wfdb，且 /tmp/mitdb 下已有对应记录的
.hea/.dat/.atr（PhysioNet 公开数据，见 README.md）。

用法：python3 export.py [记录号...]    默认跑 README 里的 17 条
"""

import os
import sys

import wfdb

DIR = os.environ.get("MITDB_DIR", "/tmp/mitdb")

# MIT-BIH 心跳标注符号集（与 PhysioNet 的 beat 分类一致）
BEAT_SYMBOLS = set("NLRBAaJSVrFejnEQf/")

DEFAULT_RECORDS = [
    100, 101, 102, 103, 104, 105, 106, 107, 108, 109,
    111, 115, 118, 119, 122, 124, 201,
]


def export(rec: int) -> bool:
    base = os.path.join(DIR, str(rec))
    try:
        record = wfdb.rdrecord(base)
        ann = wfdb.rdann(base, "atr")
    except Exception as e:  # noqa: BLE001 - 缺文件就跳过，不让整批挂掉
        print(f"{rec}: 读取失败（{e}），跳过")
        return False

    if record.p_signal is None or record.p_signal.shape[0] == 0:
        print(f"{rec}: 无信号，跳过")
        return False

    # MIT-BIH 第一导联统一是 MLII；114 号是反的，不在默认集里
    signal = record.p_signal[:, 0].astype("<f8")

    beats = [
        int(s)
        for s, sym in zip(ann.sample, ann.symbol)
        if sym in BEAT_SYMBOLS
    ]

    sig_path = base + ".sig"
    beats_path = base + ".beats"
    with open(sig_path, "wb") as f:
        f.write(signal.tobytes())
    with open(beats_path, "w") as f:
        f.write(f"fs={record.fs}\n")
        for s in beats:
            f.write(f"{s}\n")
    print(f"{rec}: {len(signal)} 点 @ {record.fs}Hz，{len(beats)} 个标注心跳")
    return True


def main() -> None:
    records = [int(x) for x in sys.argv[1:]] or DEFAULT_RECORDS
    ok = sum(export(r) for r in records)
    print(f"完成 {ok}/{len(records)}")


if __name__ == "__main__":
    main()
