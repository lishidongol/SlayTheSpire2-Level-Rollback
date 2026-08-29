package main

// 杀戮尖塔2 (Slay the Spire 2) 存档"回退到任意关卡"核心逻辑（Go 版）。
// 移植自 sts2_rollback.py，并包含 visited_map_coords 同步截断的修复：
// 游戏把"当前所在格"保存在 map_point_history（已完成格记录）与
// visited_map_coords（地图路径坐标，最后一项=当前所在格）两个成对字段中，
// 回退时必须一起截断，否则游戏读档后仍停在最新关卡。

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ---------------- 对外数据结构（wails3 绑定 / 前端使用） ----------------

type Coord struct {
	Col int `json:"col"`
	Row int `json:"row"`
}

type PlayerBrief struct {
	NetID     string       `json:"net_id"` // 字符串保存，避免 JS 大整数精度丢失
	CurrentHP int          `json:"current_hp"`
	MaxHP     int          `json:"max_hp"`
	Gold      int          `json:"gold"`
	Relics    []RelicItem  `json:"relics"`  // 遗物列表（按获得顺序）
	Potions   []PotionItem `json:"potions"` // 药水列表
	Deck      []DeckCard   `json:"deck"`    // 卡组列表（含升级等级、加入卡组的关卡）
}

type RelicItem struct {
	ID               string `json:"id"`
	FloorAddedToDeck int    `json:"floor_added_to_deck"` // 获得的关卡（1-based）
}

type PotionItem struct {
	ID        string `json:"id"`
	SlotIndex int    `json:"slot_index"` // 药水槽位
}

type DeckCard struct {
	ID                 string `json:"id"`
	CurrentUpgradeLevel int   `json:"current_upgrade_level"` // 已升级次数（0=未升级）
	FloorAddedToDeck   int    `json:"floor_added_to_deck"`   // 加入卡组的关卡（1-based）
}

type FloorStat struct {
	PlayerID  string `json:"player_id"`
	CurrentHP int    `json:"current_hp"`
	Gold      int    `json:"gold"`
}

type FloorInfo struct {
	Index  int         `json:"index"` // 1-based 格号
	Type   string      `json:"type"`
	Stats  []FloorStat `json:"stats"`
	IsLast bool        `json:"is_last"` // 是否当前幕已完成的最后一格
}

type ActInfo struct {
	Index  int         `json:"index"`  // 1-based 幕号
	Floors []FloorInfo `json:"floors"`
	// CanRollback 标记该幕是否可回退：visited_map_coords 只保留当前幕的路径坐标，
	// 更早的幕无法精确恢复路径（跨幕回退），因此仅当前幕（mph 最后一幕）可回退。
	CanRollback bool `json:"can_rollback"`
}

type SaveInfo struct {
	Path             string        `json:"path"`
	SchemaVersion    int           `json:"schema_version"`
	Ascension        int           `json:"ascension"`
	Players          []PlayerBrief `json:"players"`
	Acts             []ActInfo     `json:"acts"`
	CurrentAct       int           `json:"current_act"`      // 1-based 当前幕
	CompletedFloors  int           `json:"completed_floors"` // 当前幕已完成格数
	VisitedCoords    []Coord       `json:"visited_coords"`
	CurrentCoord     *Coord        `json:"current_coord"` // visited 最后一项（当前所在格）
	CurrentCoordType string        `json:"current_coord_type"`
	SaveTime         int64         `json:"save_time"`
	RunTime          int64         `json:"run_time"`
	StartTime        int64         `json:"start_time"`
}

type PreviewStats struct {
	NetID     string `json:"net_id"`
	CurrentHP int    `json:"current_hp"`
	MaxHP     int    `json:"max_hp"`
	Gold      int    `json:"gold"`
}

type Preview struct {
	Act             int            `json:"act"`        // 1-based 目标幕
	Floor           int            `json:"floor"`      // 1-based 目标格
	FloorType       string         `json:"floor_type"` // 目标格类型（已完成）
	CurrentAct      int            `json:"current_act"`
	CompletedFloors int            `json:"completed_floors"`
	CurrentCoord    *Coord         `json:"current_coord"` // 回退后的当前所在格
	Players         []PreviewStats `json:"players"`
	Warnings        []string       `json:"warnings"`
}

type RollbackRequest struct {
	Path   string `json:"path"`   // 存档文件路径
	Act    int    `json:"act"`    // 1-based 目标幕
	Floor  int    `json:"floor"`  // 1-based 目标格
	Mode   string `json:"mode"`   // file | overwrite | package
	Output string `json:"output"` // file: 目标文件完整路径; package: 目标父目录; overwrite: 忽略
}

type RollbackResult struct {
	OK         bool     `json:"ok"`
	Message    string   `json:"message"`
	BackupPath string   `json:"backup_path"`
	Output     string   `json:"output"`
	Preview    *Preview `json:"preview"`
	Warnings   []string `json:"warnings"`
}

// ---------------- 存档保真读写 ----------------

// loadSave 用 UseNumber 解析整个存档为 map 容器：
// 只改写少数顶层 key，其余字段值原样回写（保持 net_id 等大整数精度）。
func loadSave(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("无法打开存档文件: %w", err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.UseNumber()
	var save map[string]any
	if err := dec.Decode(&save); err != nil {
		return nil, fmt.Errorf("存档 JSON 解析失败: %w", err)
	}
	return save, nil
}

// writeSave 原子写入（先写临时文件再改名），缩进与 Python 版一致。
func writeSave(path string, save map[string]any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(save); err != nil {
		return fmt.Errorf("序列化存档失败: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	return os.Rename(tmp, path)
}

// ---------------- 类型辅助 ----------------

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func toStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	default:
		if t == nil {
			return ""
		}
		return fmt.Sprintf("%v", t)
	}
}

func toInt(v any) (int, bool) {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return int(i), true
		}
		if f, err := t.Float64(); err == nil {
			return int(f), true
		}
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	}
	return 0, false
}

func deepCopy(save map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(save)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var cp map[string]any
	if err := dec.Decode(&cp); err != nil {
		return nil, err
	}
	return cp, nil
}

// ---------------- 核心回退逻辑（不写盘） ----------------

// currentCoordType 从当前幕 saved_map 反查坐标的节点类型（仅有当前幕保留地图）。
func currentCoordType(save map[string]any, coord *Coord) string {
	actIdx, ok := toInt(save["current_act_index"])
	if !ok {
		return ""
	}
	acts := asList(save["acts"])
	if actIdx < 0 || actIdx >= len(acts) || coord == nil {
		return ""
	}
	for _, pv := range asList(asMap(asMap(acts[actIdx])["saved_map"])["points"]) {
		p := asMap(pv)
		c := asMap(p["coord"])
		col, cok := toInt(c["col"])
		row, rok := toInt(c["row"])
		if cok && rok && col == coord.Col && row == coord.Row {
			return toStr(p["type"])
		}
	}
	return ""
}

// doRollback 在内存副本上执行回退，返回新存档与预览。
func doRollback(save map[string]any, act, floor int) (map[string]any, *Preview, error) {
	mph := asList(save["map_point_history"])
	if act < 1 || act > len(mph) {
		return nil, nil, fmt.Errorf("幕越界: 只有 %d 幕", len(mph))
	}
	if act != len(mph) {
		return nil, nil, fmt.Errorf(
			"跨幕回退暂不支持: visited_map_coords 只保留当前幕(第%d幕)的路径坐标，无法恢复第%d幕的路径。可先把目标改为当前幕内的格子。",
			len(mph), act)
	}
	actFloors := asList(mph[act-1])
	if floor < 1 || floor > len(actFloors) {
		return nil, nil, fmt.Errorf("格越界: 第%d幕只有 %d 格", act, len(actFloors))
	}
	targetPt := asMap(actFloors[floor-1])

	// 玩家状态复位到"进入目标格之前"（打完目标格前一格）的状态：
	//   - 目标格不是幕首格 → 同幕前一格
	//   - 目标格是幕首格   → 上一幕最后一格（打完上一幕即进入目标格）
	//   - 目标是第1幕第1格 → 存档无更早快照，保持当前状态并提示
	var targetStats []any
	noPrior := false
	switch {
	case floor > 1:
		targetStats = asList(asMap(actFloors[floor-2])["player_stats"])
	case act > 1:
		prevAct := asList(mph[act-2])
		if len(prevAct) > 0 {
			targetStats = asList(asMap(prevAct[len(prevAct)-1])["player_stats"])
		} else {
			noPrior = true
		}
	default:
		noPrior = true
	}

	newSave, err := deepCopy(save)
	if err != nil {
		return nil, nil, fmt.Errorf("处理存档失败: %w", err)
	}

	// 1) map_point_history 截断：目标格"未打"，保留前 act-1 幕 + 当前幕前 floor-1 格
	truncated := make([]any, 0, act)
	for i := 0; i < act-1; i++ {
		truncated = append(truncated, mph[i])
	}
	newFloors := make([]any, floor-1)
	copy(newFloors, actFloors[:floor-1])
	truncated = append(truncated, newFloors)
	newSave["map_point_history"] = truncated

	// 1b) visited_map_coords 同步截断：长度为"已完成格数 (floor-1) + 1" = floor，
	//     最后一项 = 目标格坐标（当前所在格，待重打）
	if vis, ok := newSave["visited_map_coords"]; ok && asList(vis) != nil {
		visList := asList(vis)
		if floor < len(visList) {
			newSave["visited_map_coords"] = visList[:floor]
		}
	}

	// 2) current_act_index 同步到目标幕（0-based）
	newSave["current_act_index"] = act - 1

	// 3) players 的 HP/金币/上限HP 复位到目标格 player_stats
	net2stats := map[string]map[string]any{}
	for _, stV := range targetStats {
		st := asMap(stV)
		if st == nil {
			continue
		}
		net2stats[toStr(st["player_id"])] = st
	}
	for _, plV := range asList(newSave["players"]) {
		pl := asMap(plV)
		if pl == nil {
			continue
		}
		st, ok := net2stats[toStr(pl["net_id"])]
		if !ok {
			continue
		}
		if v, has := st["current_hp"]; has {
			pl["current_hp"] = v
		}
		if v, has := st["current_gold"]; has {
			pl["gold"] = v
		}
		if v, has := st["max_hp"]; has {
			pl["max_hp"] = v
		}
	}

	// 4) save_time 刷新
	newSave["save_time"] = time.Now().Unix()

	// 预览组装
	preview := &Preview{
		Act:             act,
		Floor:           floor,
		FloorType:       toStr(targetPt["map_point_type"]),
		CurrentAct:      act,
		CompletedFloors: floor - 1,
	}
	if vc, ok := newSave["visited_map_coords"]; ok {
		if vis := asList(vc); len(vis) > 0 {
			last := asMap(vis[len(vis)-1])
			if col, cok := toInt(last["col"]); cok {
				if row, rok := toInt(last["row"]); rok {
					preview.CurrentCoord = &Coord{Col: col, Row: row}
				}
			}
		}
	}
	for _, plV := range asList(newSave["players"]) {
		pl := asMap(plV)
		hp, _ := toInt(pl["current_hp"])
		maxHP, _ := toInt(pl["max_hp"])
		gold, _ := toInt(pl["gold"])
		preview.Players = append(preview.Players, PreviewStats{
			NetID:     toStr(pl["net_id"]),
			CurrentHP: hp,
			MaxHP:     maxHP,
			Gold:      gold,
		})
	}
	// 固定限制提示（目标格重打/卡组遗物/RNG/事件去重）
	preview.Warnings = append(preview.Warnings,
		fmt.Sprintf("将重打第%d幕第%d格（该格仍未完成，进游戏从它开始）。", act, floor),
		"卡组 / 遗物 / 药水不回退（存档无逐格快照），回退后保持当前状态。",
		"RNG / 随机流不回退：重进宝箱、奖励开出的内容可能与原来不同。",
		"events_seen 为全局去重：已触发过的事件（如 THIS_OR_THAT）重打时可能被跳过。",
	)
	if noPrior {
		preview.Warnings = append(preview.Warnings,
			"目标是第1幕第1格，存档没有更早的状态快照，玩家 HP/金币保持当前值。")
	}

	return newSave, preview, nil
}

// ---------------- 输出模式 ----------------

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil || rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(p, target)
	})
}

// saveRoot 从存档文件路径推断存档根目录：<root>/profileN/saves/<file>.save
func saveRoot(path string) (string, error) {
	savesDir := filepath.Dir(path)
	parent := filepath.Dir(savesDir)
	if !strings.HasPrefix(strings.ToLower(filepath.Base(parent)), "profile") {
		return "", fmt.Errorf("存档不在标准 profileN/saves 布局中，无法导出整套存档包: %s", path)
	}
	root := filepath.Dir(parent)
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return "", fmt.Errorf("无法定位存档根目录: %s", root)
	}
	return root, nil
}

// ---------------- 服务（wails3 绑定入口） ----------------

type RollbackService struct{}

// OpenSave 打开并解析存档，返回进度信息。
func (s *RollbackService) OpenSave(path string) (*SaveInfo, error) {
	save, err := loadSave(path)
	if err != nil {
		return nil, err
	}
	mph := asList(save["map_point_history"])
	info := &SaveInfo{
		Path:          path,
		SchemaVersion: mustInt(save["schema_version"]),
		Ascension:     mustInt(save["ascension"]),
		CurrentAct:    len(mph),
		SaveTime:      mustInt64(save["save_time"]),
		RunTime:       mustInt64(save["run_time"]),
		StartTime:     mustInt64(save["start_time"]),
	}
	for _, plV := range asList(save["players"]) {
		pl := asMap(plV)
		hp, _ := toInt(pl["current_hp"])
		maxHP, _ := toInt(pl["max_hp"])
		gold, _ := toInt(pl["gold"])
		brief := PlayerBrief{
			NetID:     toStr(pl["net_id"]),
			CurrentHP: hp,
			MaxHP:     maxHP,
			Gold:      gold,
		}
		// 遗物
		for _, rV := range asList(pl["relics"]) {
			r := asMap(rV)
			brief.Relics = append(brief.Relics, RelicItem{
				ID:               toStr(r["id"]),
				FloorAddedToDeck: mustInt(r["floor_added_to_deck"]),
			})
		}
		// 药水
		for _, pV := range asList(pl["potions"]) {
			p := asMap(pV)
			brief.Potions = append(brief.Potions, PotionItem{
				ID:        toStr(p["id"]),
				SlotIndex: mustInt(p["slot_index"]),
			})
		}
		// 卡组
		for _, cV := range asList(pl["deck"]) {
			c := asMap(cV)
			brief.Deck = append(brief.Deck, DeckCard{
				ID:                  toStr(c["id"]),
				CurrentUpgradeLevel: mustInt(c["current_upgrade_level"]),
				FloorAddedToDeck:    mustInt(c["floor_added_to_deck"]),
			})
		}
		info.Players = append(info.Players, brief)
	}
	for ai, actV := range mph {
		act := asList(actV)
		// 仅当前幕（mph 最后一幕）可回退
		actInfo := ActInfo{Index: ai + 1, CanRollback: ai == len(mph)-1}
		for gi, ptV := range act {
			pt := asMap(ptV)
			fi := FloorInfo{Index: gi + 1, Type: toStr(pt["map_point_type"])}
			for _, stV := range asList(pt["player_stats"]) {
				st := asMap(stV)
				hp, _ := toInt(st["current_hp"])
				gold, _ := toInt(st["current_gold"])
				fi.Stats = append(fi.Stats, FloorStat{
					PlayerID:  toStr(st["player_id"]),
					CurrentHP: hp,
					Gold:      gold,
				})
			}
			actInfo.Floors = append(actInfo.Floors, fi)
		}
		info.Acts = append(info.Acts, actInfo)
	}
	// 当前幕已完成格数 = 最后一幕的格数
	if len(mph) > 0 {
		last := asList(mph[len(mph)-1])
		info.CompletedFloors = len(last)
		if len(last) > 0 {
			info.Acts[len(info.Acts)-1].Floors[len(last)-1].IsLast = true
		}
	}
	for _, vcV := range asList(save["visited_map_coords"]) {
		vc := asMap(vcV)
		col, cok := toInt(vc["col"])
		row, rok := toInt(vc["row"])
		if cok && rok {
			info.VisitedCoords = append(info.VisitedCoords, Coord{Col: col, Row: row})
		}
	}
	if n := len(info.VisitedCoords); n > 0 {
		info.CurrentCoord = &info.VisitedCoords[n-1]
		info.CurrentCoordType = currentCoordType(save, info.CurrentCoord)
	}
	return info, nil
}

// PreviewRollback 预览回退结果（不写盘）。
func (s *RollbackService) PreviewRollback(path string, act, floor int) (*Preview, error) {
	save, err := loadSave(path)
	if err != nil {
		return nil, err
	}
	_, preview, err := doRollback(save, act, floor)
	return preview, err
}

// Rollback 执行回退并写盘。
// Mode: file=另存新文件; overwrite=覆盖原档(自动备份 .rollback_prev); package=复制整套存档目录成导入包。
func (s *RollbackService) Rollback(req RollbackRequest) (*RollbackResult, error) {
	if req.Path == "" {
		return nil, errors.New("未指定存档文件")
	}
	switch req.Mode {
	case "file", "overwrite", "package":
	default:
		return nil, fmt.Errorf("未知输出模式: %s（可选 file / overwrite / package）", req.Mode)
	}
	save, err := loadSave(req.Path)
	if err != nil {
		return nil, err
	}
	newSave, preview, err := doRollback(save, req.Act, req.Floor)
	if err != nil {
		return nil, err
	}
	res := &RollbackResult{OK: true, Preview: preview, Warnings: preview.Warnings}

	switch req.Mode {
	case "file":
		if req.Output == "" {
			return nil, errors.New("另存模式需要输出文件路径")
		}
		if err := writeSave(req.Output, newSave); err != nil {
			return nil, err
		}
		res.Output = req.Output
		res.Message = fmt.Sprintf("已生成回退档（未改动原档）: %s", req.Output)

	case "overwrite":
		bak := req.Path + ".rollback_prev"
		if err := copyFile(req.Path, bak); err != nil {
			return nil, fmt.Errorf("备份原档失败: %w", err)
		}
		if err := writeSave(req.Path, newSave); err != nil {
			return nil, err
		}
		res.BackupPath = bak
		res.Output = req.Path
		res.Message = fmt.Sprintf("已回退第%d幕第%d格并覆盖: %s（原档备份: %s）", req.Act, req.Floor, req.Path, bak)

	case "package":
		if req.Output == "" {
			return nil, errors.New("导入包模式需要输出父目录")
		}
		root, err := saveRoot(req.Path)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(root, req.Path)
		if err != nil {
			return nil, fmt.Errorf("无法定位存档在包内的相对路径: %w", err)
		}
		dirName := fmt.Sprintf("%s-rollback-%d.%d", filepath.Base(root), req.Act, req.Floor)
		dstDir := filepath.Join(req.Output, dirName)
		if err := os.RemoveAll(dstDir); err != nil {
			return nil, fmt.Errorf("清理旧导入包失败: %w", err)
		}
		if err := copyDir(root, dstDir); err != nil {
			return nil, fmt.Errorf("复制存档目录失败: %w", err)
		}
		relSave := filepath.Join(dstDir, rel)
		if err := writeSave(relSave, newSave); err != nil {
			return nil, err
		}
		res.Output = dstDir
		res.Message = fmt.Sprintf("已生成可导入的存档包: %s（内含回退后的 %s）", dstDir, rel)
	}
	return res, nil
}

// ---------------- 文件对话框（wails3） ----------------

func (s *RollbackService) PickSaveFile() (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("应用未初始化")
	}
	return app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		CanChooseFiles: true,
		Title:          "选择杀戮尖塔2存档文件",
		Filters: []application.FileFilter{
			{DisplayName: "StS2 存档 (*.save)", Pattern: "*.save"},
			{DisplayName: "JSON (*.json)", Pattern: "*.json"},
		},
	}).PromptForSingleSelection()
}

func (s *RollbackService) PickOutputFile(suggestName string) (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("应用未初始化")
	}
	return app.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
		Title:    "另存回退后的存档",
		Filename: suggestName,
		Filters: []application.FileFilter{
			{DisplayName: "StS2 存档 (*.save)", Pattern: "*.save"},
			{DisplayName: "JSON (*.json)", Pattern: "*.json"},
		},
	}).PromptForSingleSelection()
}

func (s *RollbackService) PickOutputDir() (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("应用未初始化")
	}
	return app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		CanChooseDirectories: true,
		CanChooseFiles:       false,
		Title:                "选择导入包输出目录（将在其下新建子目录）",
	}).PromptForSingleSelection()
}

// mustInt / mustInt64：取整数值，失败返回 0。
func mustInt(v any) int {
	i, _ := toInt(v)
	return i
}

func mustInt64(v any) int64 {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
	case int:
		return int64(t)
	case int64:
		return t
	case float64:
		return int64(t)
	}
	return 0
}
