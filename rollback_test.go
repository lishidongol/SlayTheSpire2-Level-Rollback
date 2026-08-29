package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 测试存档（相对本包目录 ../ 位于工作区根）
const testSave = "../sts2-save-20260828-004500/profile1/saves/current_run_mp.save"

func loadFile(t *testing.T, path string) map[string]any {
	t.Helper()
	save, err := loadSave(path)
	if err != nil {
		t.Fatalf("loadSave(%s) 失败: %v", path, err)
	}
	return save
}

func TestOpenSave(t *testing.T) {
	s, err := (&RollbackService{}).OpenSave(testSave)
	if err != nil {
		t.Fatalf("OpenSave 失败: %v", err)
	}
	if len(s.Acts) != 2 {
		t.Errorf("幕数 = %d, 期望 2", len(s.Acts))
	}
	if len(s.Acts[0].Floors) != 16 || len(s.Acts[1].Floors) != 8 {
		t.Errorf("各幕格数 = %d/%d, 期望 16/8", len(s.Acts[0].Floors), len(s.Acts[1].Floors))
	}
	if s.Acts[0].CanRollback {
		t.Error("第1幕（更早的幕）不应可回退（跨幕无法恢复路径）")
	}
	if !s.Acts[1].CanRollback {
		t.Error("第2幕（当前幕）应可回退")
	}
	if s.CurrentAct != 2 || s.CompletedFloors != 8 {
		t.Errorf("CurrentAct/CompletedFloors = %d/%d, 期望 2/8", s.CurrentAct, s.CompletedFloors)
	}
	if len(s.Players) != 2 {
		t.Fatalf("玩家数 = %d, 期望 2", len(s.Players))
	}
	// net_id 大整数精度
	wantP2 := "4147159230158730164"
	if s.Players[1].NetID != wantP2 {
		t.Errorf("P2 net_id = %s, 期望 %s（大整数精度丢失?）", s.Players[1].NetID, wantP2)
	}
	// 当前所在格坐标 = visited 最后一项 (3,8)
	if s.CurrentCoord == nil || s.CurrentCoord.Col != 3 || s.CurrentCoord.Row != 8 {
		t.Errorf("CurrentCoord = %+v, 期望 (3,8)", s.CurrentCoord)
	}
	if s.CurrentCoordType != "elite" {
		t.Errorf("CurrentCoordType = %q, 期望 elite", s.CurrentCoordType)
	}
	// 2.4 格数据
	f24 := s.Acts[1].Floors[3]
	if f24.Type != "monster" {
		t.Errorf("2.4 类型 = %s, 期望 monster", f24.Type)
	}
	if len(f24.Stats) != 2 || f24.Stats[0].CurrentHP != 54 || f24.Stats[0].Gold != 639 {
		t.Errorf("2.4 P1 stats = %+v, 期望 HP54/639g", f24.Stats)
	}
	if !s.Acts[1].Floors[7].IsLast {
		t.Error("2.8 应标记为当前幕最后一格")
	}
	// 玩家物品：遗物 / 药水 / 卡组
	p1 := s.Players[0]
	if len(p1.Relics) != 8 {
		t.Errorf("P1 遗物数 = %d, 期望 8", len(p1.Relics))
	} else {
		r0 := p1.Relics[0]
		if r0.ID != "RELIC.BURNING_BLOOD" || r0.FloorAddedToDeck != 1 {
			t.Errorf("P1 首个遗物 = %+v, 期望 BURNING_BLOOD/floor1", r0)
		}
	}
	if len(p1.Potions) != 1 || p1.Potions[0].ID != "POTION.SKILL_POTION" {
		t.Errorf("P1 药水 = %+v, 期望 POTION.SKILL_POTION", p1.Potions)
	}
	if len(p1.Deck) != 23 {
		t.Errorf("P1 卡组数 = %d, 期望 23", len(p1.Deck))
	} else {
		c0 := p1.Deck[0]
		if c0.ID != "CARD.BASH" || c0.CurrentUpgradeLevel != 1 || c0.FloorAddedToDeck != 1 {
			t.Errorf("P1 首张卡 = %+v, 期望 BASH +1级/floor1", c0)
		}
	}
	// P2 物品也应解析（双人）
	if len(s.Players[1].Relics) == 0 {
		t.Error("P2 应有遗物")
	}
}

func TestPreviewRollback(t *testing.T) {
	p, err := (&RollbackService{}).PreviewRollback(testSave, 2, 4)
	if err != nil {
		t.Fatalf("PreviewRollback(2,4) 失败: %v", err)
	}
	// 新语义：回退到 2.4 = 重打 2.4，2.4 未完成，前 3 格已完成
	if p.FloorType != "monster" || p.CompletedFloors != 3 {
		t.Errorf("FloorType/CompletedFloors = %s/%d, 期望 monster/3", p.FloorType, p.CompletedFloors)
	}
	if p.CurrentCoord == nil || p.CurrentCoord.Col != 4 || p.CurrentCoord.Row != 3 {
		t.Errorf("回退后 CurrentCoord = %+v, 期望 (4,3)=2.4 monster（待重打）", p.CurrentCoord)
	}
	if len(p.Players) != 2 || p.Players[0].CurrentHP != 62 || p.Players[0].Gold != 624 ||
		p.Players[1].CurrentHP != 71 || p.Players[1].Gold != 184 {
		t.Errorf("回退后玩家状态 = %+v, 期望 62/624 与 71/184（2.3 打完、进入2.4前）", p.Players)
	}
	found := false
	for _, w := range p.Warnings {
		if strings.Contains(w, "将重打第2幕第4格") {
			found = true
		}
	}
	if !found {
		t.Error("应包含“将重打”提示")
	}
}

// 回退到 2.4（重打）的完整输出断言（与旧 Python 版 fixed-2.4 语义不同：
// 新语义下 2.4 未打，mp 保留前 3 格、路径停在 2.4、状态复位到 2.3 打完）
func TestRollbackFileExpectations(t *testing.T) {
	svc := &RollbackService{}
	out := filepath.Join(t.TempDir(), "current_run_mp.save")
	_, err := svc.Rollback(RollbackRequest{Path: testSave, Act: 2, Floor: 4, Mode: "file", Output: out})
	if err != nil {
		t.Fatalf("Rollback(mode=file) 失败: %v", err)
	}
	got := loadFile(t, out)

	if gj := len(asList(got["map_point_history"])); gj != 2 {
		t.Fatalf("mph 幕数 = %d, 期望 2", gj)
	}
	gAct := asList(asList(got["map_point_history"])[1])
	if len(gAct) != 3 {
		t.Fatalf("mph 第2幕格数 = %d, 期望 3（2.1-2.3 已完成，2.4 待重打）", len(gAct))
	}
	gVis := asList(got["visited_map_coords"])
	if len(gVis) != 4 {
		t.Fatalf("visited 项数 = %d, 期望 4", len(gVis))
	}
	last := asMap(gVis[len(gVis)-1])
	if col, _ := toInt(last["col"]); col != 4 {
		t.Errorf("visited 末项 col = %d, 期望 4（2.4 坐标）", col)
	}
	if row, _ := toInt(last["row"]); row != 3 {
		t.Errorf("visited 末项 row = %d, 期望 3", row)
	}
	if gci, _ := toInt(got["current_act_index"]); gci != 1 {
		t.Errorf("current_act_index = %d, 期望 1", gci)
	}
	// 玩家状态 = 2.3 打完（进入 2.4 前）
	gPl := asList(got["players"])
	if hp, _ := toInt(asMap(gPl[0])["current_hp"]); hp != 62 {
		t.Errorf("P1 HP = %d, 期望 62", hp)
	}
	if gold, _ := toInt(asMap(gPl[0])["gold"]); gold != 624 {
		t.Errorf("P1 gold = %d, 期望 624", gold)
	}
	if hp, _ := toInt(asMap(gPl[1])["current_hp"]); hp != 71 {
		t.Errorf("P2 HP = %d, 期望 71", hp)
	}
	if gold, _ := toInt(asMap(gPl[1])["gold"]); gold != 184 {
		t.Errorf("P2 gold = %d, 期望 184", gold)
	}
	// net_id 精度保持
	if id := toStr(asMap(gPl[1])["net_id"]); id != "4147159230158730164" {
		t.Errorf("P2 net_id = %s, 精度丢失", id)
	}
	// 生成档可被重新解析（合法 JSON + UTF-8）
	if _, err := loadSave(out); err != nil {
		t.Fatalf("生成档无法重新解析: %v", err)
	}
	// 未改动的字段应保留（抽查 acts）
	if len(asList(got["acts"])) != 3 {
		t.Errorf("acts 应保留 3 幕，实际 %d", len(asList(got["acts"])))
	}
}

// 回退到幕首格（2.1）：第2幕 0 格已完成，路径停在 2.1 起始格 (3,0)，
// 玩家状态复位到打完上一幕（1.16 boss）后
func TestRollbackToFirstFloorOfAct(t *testing.T) {
	svc := &RollbackService{}
	out := filepath.Join(t.TempDir(), "current_run_mp.save")
	_, err := svc.Rollback(RollbackRequest{Path: testSave, Act: 2, Floor: 1, Mode: "file", Output: out})
	if err != nil {
		t.Fatalf("Rollback(2,1) 失败: %v", err)
	}
	got := loadFile(t, out)
	gAct := asList(asList(got["map_point_history"])[1])
	if len(gAct) != 0 {
		t.Fatalf("mph 第2幕格数 = %d, 期望 0（2.1 待重打）", len(gAct))
	}
	gVis := asList(got["visited_map_coords"])
	if len(gVis) != 1 {
		t.Fatalf("visited 项数 = %d, 期望 1", len(gVis))
	}
	last := asMap(gVis[0])
	if col, _ := toInt(last["col"]); col != 3 {
		t.Errorf("visited 末项 col = %d, 期望 3（2.1 起始格）", col)
	}
	if row, _ := toInt(last["row"]); row != 0 {
		t.Errorf("visited 末项 row = %d, 期望 0", row)
	}
	// 状态从上一幕末格（1.16 boss 后）：9/552g 与 63/174g
	gPl := asList(got["players"])
	if hp, _ := toInt(asMap(gPl[0])["current_hp"]); hp != 9 {
		t.Errorf("P1 HP = %d, 期望 9（1.16 打完）", hp)
	}
	if gold, _ := toInt(asMap(gPl[0])["gold"]); gold != 552 {
		t.Errorf("P1 gold = %d, 期望 552", gold)
	}
	if hp, _ := toInt(asMap(gPl[1])["current_hp"]); hp != 63 {
		t.Errorf("P2 HP = %d, 期望 63", hp)
	}
	if gold, _ := toInt(asMap(gPl[1])["gold"]); gold != 174 {
		t.Errorf("P2 gold = %d, 期望 174", gold)
	}
}

func TestRollbackCrossActRejected(t *testing.T) {
	_, err := (&RollbackService{}).PreviewRollback(testSave, 1, 5)
	if err == nil || !strings.Contains(err.Error(), "跨幕回退暂不支持") {
		t.Fatalf("跨幕回退应被拦截，实际 err = %v", err)
	}
}

func TestRollbackOutOfRange(t *testing.T) {
	if _, err := (&RollbackService{}).PreviewRollback(testSave, 2, 99); err == nil {
		t.Error("格越界应报错")
	}
	if _, err := (&RollbackService{}).PreviewRollback(testSave, 99, 1); err == nil {
		t.Error("幕越界应报错")
	}
}

// overwrite 模式：备份 + 覆盖
func TestRollbackOverwrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "current_run_mp.save")
	raw, err := os.ReadFile(testSave)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := (&RollbackService{}).Rollback(RollbackRequest{Path: src, Act: 2, Floor: 4, Mode: "overwrite"})
	if err != nil {
		t.Fatalf("Rollback(overwrite) 失败: %v", err)
	}
	if res.BackupPath != src+".rollback_prev" {
		t.Errorf("备份路径 = %s", res.BackupPath)
	}
	if _, err := os.Stat(src + ".rollback_prev"); err != nil {
		t.Errorf("备份文件不存在: %v", err)
	}
	// 原档已覆盖
	got := loadFile(t, src)
	if len(asList(asList(got["map_point_history"])[1])) != 3 {
		t.Error("覆盖后 mph 第2幕应为 3 格（2.4 待重打）")
	}
}

// package 模式：复制整套存档目录成导入包
func TestRollbackPackage(t *testing.T) {
	outParent := t.TempDir()
	res, err := (&RollbackService{}).Rollback(RollbackRequest{
		Path: testSave, Act: 2, Floor: 4, Mode: "package", Output: outParent,
	})
	if err != nil {
		t.Fatalf("Rollback(package) 失败: %v", err)
	}
	if !strings.HasSuffix(res.Output, "sts2-save-20260828-004500-rollback-2.4") {
		t.Errorf("导入包目录名 = %s", res.Output)
	}
	// 包内所有文件和结构应被复制
	for _, rel := range []string{
		"profile.save",
		"profile1/saves/current_run_mp.save",
		"profile1/saves/current_run_mp.save.backup",
		"profile1/saves/current_run.save",
		"profile1/replays/latest.mcr",
	} {
		if _, err := os.Stat(filepath.Join(res.Output, rel)); err != nil {
			t.Errorf("包内缺失 %s", rel)
		}
	}
	// 包内存档已回退
	got := loadFile(t, filepath.Join(res.Output, "profile1/saves/current_run_mp.save"))
	if len(asList(asList(got["map_point_history"])[1])) != 3 {
		t.Error("包内存档 mph 第2幕应为 3 格")
	}
	if len(asList(got["visited_map_coords"])) != 4 {
		t.Error("包内存档 visited 应为 4 项")
	}
	// 原档未被改动（mp 仍 8 格）
	orig := loadFile(t, testSave)
	if len(asList(asList(orig["map_point_history"])[1])) != 8 {
		t.Error("原档不应被修改")
	}
}

// saveRoot 布局推断
func TestSaveRoot(t *testing.T) {
	root, err := saveRoot(testSave)
	if err != nil {
		t.Fatalf("saveRoot 失败: %v", err)
	}
	if filepath.Base(root) != "sts2-save-20260828-004500" {
		t.Errorf("root = %s", root)
	}
	if _, err := saveRoot(filepath.Join(t.TempDir(), "weird.save")); err == nil {
		t.Error("非标准布局应报错")
	}
}