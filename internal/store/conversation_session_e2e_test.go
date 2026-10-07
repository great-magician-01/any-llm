package store

import (
	"encoding/json"
	"testing"
	"time"
)

// 会话聚合分表在 PG 与 MySQL 上各跑一遍，覆盖两种方言都必须成立的规则：
//   - 首轮 upsert 自动建当月分表（含共享序列 / id_sequences 计数器）；
//   - 跨月续聊更新**首轮月份**的原行，不在新月分表建行；
//   - session_id 跨分表查找、last_response_id 链定位；
//   - 列表跨分表分页与详情跨分表扇出。

func TestSessionShardsE2E(t *testing.T) {
	for _, dl := range cseDialects {
		dl := dl
		t.Run(dl.name, func(t *testing.T) {
			d := cseTestDB(t, dl)
			resetShardCache(t, sessShards)
			if err := LoadSessionShards(d); err != nil {
				t.Fatalf("load session shards: %v", err)
			}

			now := time.Now()
			month1 := time.Date(now.Year(), now.Month(), 1, 12, 0, 0, 0, time.Local).AddDate(0, -1, 0)
			month2 := month1.AddDate(0, 1, 0)

			full1 := msgJSON(`{"Role":"user","Content":[{"Type":"text","Text":"hi"}]}`)
			asst1 := json.RawMessage(`{"Role":"assistant","Content":[{"Type":"text","Text":"hello"}]}`)

			// 首轮落在 month1 分表
			turn1 := turnOK("cc:e2e", full1, nil, asst1)
			turn1.Now = month1
			if err := UpsertSessionTurn(d, turn1); err != nil {
				t.Fatalf("turn1: %v", err)
			}
			var shard1 int
			if err := d.QueryRow(`SELECT COUNT(*) FROM ` + SessionShardName(month1)).Scan(&shard1); err != nil || shard1 != 1 {
				t.Fatalf("month1 shard rows=%d err=%v", shard1, err)
			}

			// 跨月续聊：更新 month1 的原行，month2 分表里没有它
			full2 := msgJSON(
				`{"Role":"user","Content":[{"Type":"text","Text":"hi"}]}`,
				`{"Role":"assistant","Content":[{"Type":"text","Text":"hello"}]}`,
				`{"Role":"user","Content":[{"Type":"text","Text":"again"}]}`,
			)
			turn2 := turnOK("cc:e2e", full2, nil, asst1)
			turn2.Now = month2
			if err := UpsertSessionTurn(d, turn2); err != nil {
				t.Fatalf("turn2: %v", err)
			}
			var turnCount int
			if err := d.QueryRow(`SELECT turn_count FROM ` + SessionShardName(month1) + ` WHERE session_id='cc:e2e'`).Scan(&turnCount); err != nil {
				t.Fatalf("read back: %v", err)
			}
			if turnCount != 2 {
				t.Fatalf("turn_count=%d, want 2（跨月续聊更新原行）", turnCount)
			}

			// 另一个会话落在 month2
			turn3 := turnOK("hdr:e2e", full1, nil, asst1)
			turn3.Now = month2
			if err := UpsertSessionTurn(d, turn3); err != nil {
				t.Fatalf("turn3: %v", err)
			}

			// responses 链：首轮无 pid，续接轮按 last_response_id 归到同一会话
			rt1 := turnOK("", nil, full1, asst1)
			rt1.OwnRespID = "resp_e2e_1"
			rt1.Now = month2
			if err := UpsertSessionTurn(d, rt1); err != nil {
				t.Fatalf("resp turn1: %v", err)
			}
			rt2 := turnOK("", nil, full1, asst1)
			rt2.PrevResponseID = "resp_e2e_1"
			rt2.OwnRespID = "resp_e2e_2"
			rt2.Now = month2
			if err := UpsertSessionTurn(d, rt2); err != nil {
				t.Fatalf("resp turn2: %v", err)
			}
			var respTurns int
			if err := d.QueryRow(`SELECT turn_count FROM ` + SessionShardName(month2) + ` WHERE session_id='resp:resp_e2e_1'`).Scan(&respTurns); err != nil || respTurns != 2 {
				t.Fatalf("resp chain turn_count=%d err=%v, want 2", respTurns, err)
			}

			// 列表：两个分表共 3 个会话；分页不漏不重
			list, total, err := ConversationSessionsList(d, 1, 50)
			if err != nil || total != 3 || len(list) != 3 {
				t.Fatalf("list total=%d len=%d err=%v", total, len(list), err)
			}
			var paged []string
			for page := 1; page <= 3; page++ {
				rows, _, err := ConversationSessionsList(d, page, 1)
				if err != nil || len(rows) != 1 {
					t.Fatalf("page %d: len=%d err=%v", page, len(rows), err)
				}
				paged = append(paged, rows[0].SessionID)
			}
			seen := map[string]bool{}
			for _, sid := range paged {
				if seen[sid] {
					t.Fatalf("duplicate across pages: %v", paged)
				}
				seen[sid] = true
			}
			if len(seen) != 3 {
				t.Fatalf("paged=%v", paged)
			}

			// 详情跨分表按 id 扇出（id 全局唯一：PG 序列 / MySQL 计数器）
			ids := map[int64]bool{}
			for _, s := range list {
				if ids[s.ID] {
					t.Fatalf("duplicate id %d across shards", s.ID)
				}
				ids[s.ID] = true
				detail, err := GetConversationSession(d, s.ID)
				if err != nil {
					t.Fatalf("get %d: %v", s.ID, err)
				}
				if !json.Valid([]byte(detail.Messages)) {
					t.Fatalf("session %s messages invalid JSON: %q", s.SessionID, detail.Messages)
				}
			}
		})
	}
}
