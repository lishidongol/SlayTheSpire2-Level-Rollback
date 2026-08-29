# StS2 Rollback — 杀戮尖塔2 存档回退工具（Wails3 GUI）

将《杀戮尖塔2》(Slay the Spire 2) 的单人/双人存档**回退到任意关卡**的桌面工具。
由 wails3 + Go + Vue3 构建，移植自 `sts2_rollback.py` 并包含其修复逻辑。

## 功能

- 打开存档（`current_run_mp.save` / `current_run.save`，schema 14+），图形化列出全部幕/格进度
  （格类型、每格各玩家 HP/金币、当前所在格坐标与节点类型）。
- 点击任意格作为回退目标，先「计算回退结果」预览：
  - **回退到第 X 格 = 重打第 X 格**：该格未完成，进游戏从它开始（地图路径停在 X 的坐标）
  - 玩家 HP / 上限 HP / 金币复位到「进入第 X 格之前」的状态（即打完 X 前一格；
    目标是幕首格时取上一幕最后一格；目标是第1幕第1格时无快照，保持当前值并提示）
  - 已知限制提示
- 三种输出方式：
  1. **另存为新文件**（不动原档，系统保存对话框）
  2. **覆盖原档 + 自动备份**（原档先备份为 `<存档>.rollback_prev`）
  3. **复制整套存档目录成导入包**（自动生成 `<存档目录名>-rollback-幕.格` 子目录，
     只有其中的当前 run 文件被回退，其余文件原样复制，可直接作为存档目录导入游戏）

## 回退原理（重要）

游戏把「当前所在格」保存在**成对**的两个字段中，回退时必须一起截断：

| 字段 | 含义 |
|---|---|
| `map_point_history` | 已完成格的记录（每格含 `map_point_type`、`player_stats`、`rooms`） |
| `visited_map_coords` | 当前幕地图上的路径坐标，最后一项 = 当前所在格 |

- 若只截断 `map_point_history` 而不动 `visited_map_coords`，游戏读档后仍会定位到最新关卡
  （这是早期脚本版本不生效的根因，本工具已修复）。
- 规则：回退到第 X 格重打时，`map_point_history` 保留到 X 前一格（X 未完成），
  `visited_map_coords` 保留到 X（最后一项 = X 的坐标，即当前所在格），两者长度关系保持
  「已完成格数 N ↔ visited N+1 项」。
- 回退同时把玩家 HP / 金币 / 上限 HP 复位为目标格**前一格** `player_stats` 中的值，
  并同步 `current_act_index` 与 `save_time`。

## 已知限制

- **跨幕回退暂不支持**：`visited_map_coords` 只保留当前幕的路径坐标，更早幕的坐标
  已随进入新幕被游戏丢弃，无法精确恢复（GUI 会拦截并提示）。
- **卡组 / 遗物 / 药水不回退**：存档没有逐格快照，回退后保持当前状态。
- **RNG / 随机流不回退**：回退后重进宝箱、奖励开出的内容可能与原来不同。
- `events_seen` 为全局去重：已触发过的事件（如 `THIS_OR_THAT`）重打时可能被跳过。

## 开发与构建

环境要求：Go 1.25+、Node 18+、`wails3` CLI（`go install github.com/wailsapp/wails/v3/cmd/wails3@latest`）。

```
# 运行（热重载开发模式）
wails3 dev

# 类型检查 + 生产构建（产出 bin/slay-the-spire-2.exe）
wails3 build
# 仅前端生产构建（产出 frontend/dist）
cd frontend && npm run build

# 后端单元测试（用仓库旁的 sts2-save-* 存档做真实数据回归）
go test ./
```

- 前端绑定：`wails3 generate bindings`（build 流程自动执行，输出 `frontend/bindings/sts2rollback/*.ts`）。



![img](.\imgs\img.png)![img](.\imgs\img_1.png)
