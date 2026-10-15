-- 知常 · 把「未定论」的心电判定从 unknown 搬回 inconclusive
--
-- 分类器此前不认识 iOS 简中的「未定论」，这些行落到 unknown；
-- 只改 Go 侧的话，历史行会与新入库的行分裂成两组（按判定统计时那几条凭空消失），
-- 而这正是这条链路一开始要解决的问题。
--
-- 刻意只搬「新增关键字」命中的行，不重算全部：完整判定逻辑在 Go 的
-- domain/ecg.Classify 里，在 SQL 里再抄一份就是第二套事实来源，
-- 两边一旦漂移，谁都不知道哪条记录的结论是对的。
-- 原文（classification）一直原样存着，所以这次搬迁可核对、可重放。

UPDATE ecg_records
   SET classification_code = 'inconclusive'
 WHERE classification_code = 'unknown'
   AND (classification LIKE '%未定论%'
     OR classification LIKE '%未定論%'
     OR classification LIKE '%无法定论%'
     OR classification LIKE '%無法定論%'
     OR classification LIKE '%结论待定%'
     OR classification LIKE '%結論待定%');
