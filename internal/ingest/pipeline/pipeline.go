// Package pipeline 编排一次摄入：归一化 → 去重入库 → 登记审计 → 触发聚合重算。
package pipeline

import (
	"context"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/ingest/hae"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// Service 承担摄入编排。
type Service struct {
	db      *postgres.DB
	metrics *metric.Registry
	home    *time.Location
	mode    string // strict | latest
}

// New 构造摄入服务。
func New(db *postgres.DB, reg *metric.Registry, home *time.Location, dedupMode string) *Service {
	if dedupMode == "" {
		dedupMode = "strict"
	}
	return &Service{db: db, metrics: reg, home: home, mode: dedupMode}
}

// Outcome 是一次摄入的完整产出。
type Outcome struct {
	postgres.IngestStats
	UnknownMetrics []string
	Skipped        int
}

// Ingest 执行一次摄入；同一 session-id 重复提交时直接返回上次结果。
func (s *Service) Ingest(ctx context.Context, payload *hae.Payload, meta postgres.RunMeta) (*Outcome, error) {
	runID, duplicate, err := s.db.BeginRun(ctx, meta)
	if err != nil {
		return nil, err
	}
	if duplicate {
		prev, err := s.db.LoadRunStats(ctx, runID)
		if err != nil {
			return nil, err
		}
		prev.DuplicateRun = true
		return &Outcome{IngestStats: prev}, nil
	}

	out := &Outcome{IngestStats: postgres.IngestStats{RunID: runID}}
	runErr := s.run(ctx, runID, payload, meta, out)
	if runErr != nil {
		out.IngestStats.RunID = runID
		_ = s.db.FinishRun(ctx, runID, out.IngestStats, runErr)
		return out, runErr
	}
	if err := s.db.FinishRun(ctx, runID, out.IngestStats, nil); err != nil {
		return out, err
	}
	return out, nil
}

func (s *Service) run(ctx context.Context, runID int64, payload *hae.Payload, meta postgres.RunMeta, out *Outcome) error {
	res := hae.Normalize(payload, s.metrics, s.home)
	out.Received = payload.Count()
	out.UnknownMetrics = res.Unknown
	out.Skipped = res.Skipped

	sources := make(map[string]struct{})
	dates := make(map[string]map[string]struct{})
	marks := make(map[string]postgres.Watermark)
	addDate := func(date, m string) {
		if date == "" || m == "" {
			return
		}
		if dates[date] == nil {
			dates[date] = map[string]struct{}{}
		}
		dates[date][m] = struct{}{}
	}
	for _, sm := range res.Samples {
		if sm.Source != "" {
			sources[sm.Source] = struct{}{}
		}
		addDate(sm.LocalDate, sm.Metric)
		if cur, ok := marks[sm.Metric]; !ok || sm.StartTime.After(cur.Time) {
			marks[sm.Metric] = postgres.Watermark{Time: sm.StartTime, LocalDate: sm.LocalDate}
		}
	}
	for _, w := range res.Workouts {
		if w.Source != "" {
			sources[w.Source] = struct{}{}
		}
	}
	// 睡眠与心电的来源不登记进 sources：那张表是「谁产出了 samples」的清单，
	// 列表页按 samples 计数，把只产事件表的来源写进去只会多出一行「0 条样本」，
	// 看着像故障。心电的来源名（实测是「移动心电图房颤提示软件」）随记录一同展示。

	inserted, updated, from, to, err := s.db.WriteSamples(ctx, runID, res.Samples, s.mode)
	if err != nil {
		return err
	}
	out.Inserted, out.Updated, out.AffectedFrom, out.AffectedTo = inserted, updated, from, to
	out.Duplicates = len(res.Samples) - inserted

	if n, err := s.db.WriteWorkouts(ctx, runID, res.Workouts); err != nil {
		return err
	} else {
		out.Workouts = n
	}
	if n, err := s.db.WriteSleep(ctx, runID, res.Sleep); err != nil {
		return err
	} else {
		out.Sleep = n
	}
	if n, err := s.db.WriteECG(ctx, runID, res.ECG); err != nil {
		return err
	} else {
		out.ECG = n
	}

	sourceList := make([]string, 0, len(sources))
	for k := range sources {
		sourceList = append(sourceList, k)
	}
	if err := s.db.UpsertSources(ctx, sourceList); err != nil {
		return err
	}
	if meta.AutomationID != "" && len(marks) > 0 {
		if err := s.db.UpdateWatermark(ctx, meta.AutomationID, marks); err != nil {
			return err
		}
	}
	if len(dates) > 0 {
		if err := s.db.EnqueueRollup(ctx, dates); err != nil {
			return err
		}
	}
	return nil
}
