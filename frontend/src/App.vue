<script setup lang="ts">
import { computed, ref } from 'vue'
import { RollbackService } from '@bindings/sts2rollback'
import type {
  SaveInfo, FloorInfo, Preview, RollbackResult,
} from '@bindings/sts2rollback'

const TYPE_LABELS: Record<string, string> = {
  ancient: '远古', monster: '怪物', unknown: '未知', rest_site: '休息',
  treasure: '宝箱', elite: '精英', shop: '商店', boss: '首领', event: '事件',
}

// ---------- 状态 ----------
const pathInput = ref('')
const info = ref<SaveInfo | null>(null)
const loading = ref(false)
const errorMsg = ref('')

const selected = ref<{ act: number; floor: number } | null>(null)
const preview = ref<Preview | null>(null)
const mode = ref<'file' | 'overwrite' | 'package'>('file')
const outputPath = ref('')
const result = ref<RollbackResult | null>(null)
const activeTab = ref<'floors' | 'players'>('floors')

// ---------- 打开存档 ----------
async function browse() {
  const p = await RollbackService.PickSaveFile()
  if (p) openSave(p)
}

async function openSave(p?: string) {
  const path = p ?? pathInput.value.trim()
  if (!path) { errorMsg.value = '请先选择或输入存档文件路径'; return }
  loading.value = true
  errorMsg.value = ''
  result.value = null
  preview.value = null
  try {
    info.value = await RollbackService.OpenSave(path)
    pathInput.value = info.value?.path ?? path
  } catch (e: any) {
    errorMsg.value = e?.message ?? String(e)
    info.value = null
  } finally {
    loading.value = false
  }
}

// ---------- 选区与预览 ----------
const selectedFloor = computed<FloorInfo | null>(() => {
  const sel = selected.value
  if (!sel) return null
  const inf = info.value
  if (!inf) return null
  const act = (inf.acts ?? []).find(a => a.index === sel.act)
  return (act?.floors ?? []).find((f) => f.index === sel.floor) ?? null
})

function selectFloor(actIndex: number, floorIndex: number) {
  // 只有当前幕可回退（后端兜底，前端同样拦截）
  const act = (info.value?.acts ?? []).find(a => a.index === actIndex)
  if (!act?.can_rollback) return
  selected.value = { act: actIndex, floor: floorIndex }
  preview.value = null
  result.value = null
  doPreview()
}

async function doPreview() {
  if (!pathInput.value || !selected.value) return
  loading.value = true
  errorMsg.value = ''
  try {
    preview.value = await RollbackService.PreviewRollback(
      pathInput.value, selected.value.act, selected.value.floor)
  } catch (e: any) {
    errorMsg.value = e?.message ?? String(e)
    preview.value = null
  } finally {
    loading.value = false
  }
}

// ---------- 输出路径 ----------
const suggestName = computed(() => {
  const base = pathInput.value.split(/[\\/]/).pop() ?? 'current_run_mp.save'
  const dot = base.lastIndexOf('.')
  const stem = dot > 0 ? base.slice(0, dot) : base
  const t = selected.value ? `${selected.value.act}.${selected.value.floor}` : 'x.y'
  return `${stem}.rollback_${t}.save`
})

async function pickOutput() {
  if (mode.value === 'file') {
    const p = await RollbackService.PickOutputFile(suggestName.value)
    if (p) outputPath.value = p
  } else if (mode.value === 'package') {
    const d = await RollbackService.PickOutputDir()
    if (d) outputPath.value = d
  }
}

function setMode(m: 'file' | 'overwrite' | 'package') {
  mode.value = m
  if (m === 'overwrite') outputPath.value = ''
  result.value = null
}

// ---------- 执行回退 ----------
const canRollback = computed(() => {
  if (!selected.value) return false
  if (mode.value === 'overwrite') return true
  return outputPath.value !== ''
})

async function doRollback() {
  if (!selected.value) return
  loading.value = true
  errorMsg.value = ''
  result.value = null
  try {
    result.value = await RollbackService.Rollback({
      path: pathInput.value,
      act: selected.value.act,
      floor: selected.value.floor,
      mode: mode.value,
      output: mode.value === 'overwrite' ? '' : outputPath.value,
    })
  } catch (e: any) {
    errorMsg.value = e?.message ?? String(e)
  } finally {
    loading.value = false
  }
}

// ---------- 小工具 ----------
const modeLabel: Record<string, string> = {
  file: '另存为新文件（不动原档）',
  overwrite: '覆盖原档 + 自动备份 .rollback_prev',
  package: '复制整套存档目录成可导入的存档包',
}
const typeLabel = (t?: string) => TYPE_LABELS[t ?? ''] ?? t ?? ''
const fmtTs = (s: number) => (s ? new Date(s * 1000).toLocaleString() : '—')
// 显示短 ID：RELIC.BURNING_BLOOD -> BURNING_BLOOD（完整 ID 放 title）
const shortId = (id: string) => id.split('.').slice(1).join('.') || id
</script>

<template>
  <div class="shell">
    <header class="topbar">
      <div class="brand">
        <span class="logo">⌛</span>
        <div>
          <h1>StS2 存档回退工具</h1>
          <p>杀戮尖塔2 单/双人存档 · 回退到任意关卡</p>
        </div>
      </div>
      <button class="btn primary" :disabled="loading" @click="browse">打开存档…</button>
    </header>

    <section class="filebar card">
      <input v-model="pathInput" class="text" :title="'存档文件路径，例如 …\\profile1\\saves\\current_run_mp.save'"
             placeholder="存档文件路径（可粘贴完整路径）" @keyup.enter="openSave()" />
      <button class="btn" :disabled="loading" @click="browse">浏览</button>
      <button class="btn" :disabled="loading" @click="openSave()">载入</button>
    </section>

    <p v-if="errorMsg" class="error card">{{ errorMsg }}</p>

    <!-- 元信息 -->
    <section v-if="info" class="meta card">
      <span class="chip">Schema v{{ info.schema_version }}</span>
      <span class="chip">进阶 {{ info.ascension }}</span>
      <span class="chip">当前第 {{ info.current_act }} 幕 · 已完成 {{ info.completed_floors }} 格</span>
      <span v-if="info.current_coord" class="chip">
        当前位置 ({{ info.current_coord.col }}, {{ info.current_coord.row }})<template v-if="info.current_coord_type"> · {{ typeLabel(info.current_coord_type) }}</template>
      </span>
      <span class="chip">存档时间 {{ fmtTs(info.save_time) }}</span>
      <div class="players">
        <span v-for="p in info.players ?? []" :key="p.net_id" class="player chip">
          P{{ p.net_id }} · {{ p.current_hp }}/{{ p.max_hp }}HP · {{ p.gold }}g
        </span>
      </div>
    </section>

    <!-- Tab 切换：关卡进度 / 玩家信息 -->
    <nav v-if="info" class="tabs">
      <button class="tab" :class="{ on: activeTab === 'floors' }" @click="activeTab = 'floors'">关卡进度</button>
      <button class="tab" :class="{ on: activeTab === 'players' }" @click="activeTab = 'players'">玩家信息</button>
    </nav>

    <!-- 玩家物品详情：遗物 / 药水 / 卡组 -->
    <section v-if="info && activeTab === 'players'" class="pdetails card">
      <h2>玩家物品</h2>
      <div class="pcols">
        <div v-for="p in info.players ?? []" :key="p.net_id" class="pcol">
          <h3>
            玩家 P{{ p.net_id }}
            <span class="phpmax">{{ p.current_hp }}/{{ p.max_hp }}HP · {{ p.gold }}g</span>
          </h3>
          <details open class="group">
            <summary>遗物（{{ (p.relics ?? []).length }}）</summary>
            <ul class="items">
              <li v-for="r in (p.relics ?? [])" :key="r.id" :title="r.id">
                {{ shortId(r.id) }}
                <em v-if="r.floor_added_to_deck">第{{ r.floor_added_to_deck }}格</em>
              </li>
            </ul>
          </details>
          <details class="group">
            <summary>药水（{{ (p.potions ?? []).length }}）</summary>
            <ul class="items">
              <li v-for="po in (p.potions ?? [])" :key="po.id" :title="po.id">
                {{ shortId(po.id) }}
                <em>槽{{ po.slot_index }}</em>
              </li>
            </ul>
          </details>
          <details class="group">
            <summary>卡组（{{ (p.deck ?? []).length }}）</summary>
            <div class="cards">
              <span v-for="c in (p.deck ?? [])" :key="c.id + '#' + c.floor_added_to_deck"
                    class="card" :class="{ up: c.current_upgrade_level > 0 }" :title="c.id">
                {{ shortId(c.id) }}<template v-if="c.current_upgrade_level > 0"> +{{ c.current_upgrade_level }}</template>
                <i>第{{ c.floor_added_to_deck }}格</i>
              </span>
            </div>
          </details>
        </div>
      </div>
    </section>

    <!-- 关卡进度：进度列表 + 回退设置 -->
    <main v-if="info && activeTab === 'floors'" class="cols">
      <section class="floors card">
        <h2>进度</h2>
        <div v-for="act in info.acts ?? []" :key="act.index" class="act">
          <h3>
            第 {{ act.index }} 幕（{{ act.floors?.length ?? 0 }} 格）
            <span v-if="!act.can_rollback" class="lock">已通关 · 不可回退</span>
          </h3>
          <div class="grid">
            <button v-for="f in (act.floors ?? [])" :key="f.index" class="floor"
                    :disabled="!act.can_rollback"
                    :class="{
                      sel: selected && selected.act === act.index && selected.floor === f.index,
                      last: f.is_last,
                      locked: !act.can_rollback,
                    }"
                    @click="selectFloor(act.index, f.index)"
                    :title=" act.can_rollback ? `第${act.index}幕第${f.index}格（可回退）` : `第${act.index}幕第${f.index}格（已通关，不可回退）`">
              <span class="idx">{{ act.index }}.{{ f.index }}</span>
              <span class="type">{{ typeLabel(f.type) }}</span>
              <span v-for="s in (f.stats ?? [])" :key="s.player_id" class="stat">
                P{{ s.player_id }}: {{ s.current_hp }}HP/{{ s.gold }}g
              </span>
              <span v-if="f.is_last" class="badge">当前</span>
            </button>
          </div>
        </div>
        <p class="hint">仅当前幕（第 {{ info.current_act }} 幕）的格可以回退；更早的幕已通关、无法恢复路径。带「当前」标记的是已完成的最后一格。</p>
      </section>

      <aside class="panel card">
        <h2>回退设置</h2>

        <div class="target">
          <label>目标格（将原地重打）</label>
          <div v-if="selectedFloor" class="target-val">
            第 {{ selected?.act }} 幕第 {{ selected?.floor }} 格
            <span class="type">{{ typeLabel(selectedFloor.type) }}</span>
            <span class="tip">回退后从这一格开始</span>
          </div>
          <span v-else class="muted">（尚未选择）</span>
        </div>

        <div class="target">
          <label>预览</label>
          <button class="btn" :disabled="!selected || loading" @click="doPreview">计算回退结果</button>
        </div>

        <div v-if="preview" class="preview">
          <div class="preview-grid">
            <span>进游戏后位置（重打起点）</span>
            <b v-if="preview.current_coord">({{ preview.current_coord.col }}, {{ preview.current_coord.row }})</b>
            <b v-else>—</b>
            <span>已完成格数</span><b>{{ preview.completed_floors }}（第 {{ preview.current_act }} 幕）</b>
            <span>玩家状态</span>
            <div class="stats">
              <div v-for="p in (preview.players ?? [])" :key="p.net_id" class="statline">
                P{{ p.net_id }}：{{ p.current_hp }}/{{ p.max_hp }}HP · {{ p.gold }}g
              </div>
            </div>
          </div>
          <ul v-if="preview.warnings?.length" class="warnings">
            <li v-for="(w, i) in (preview.warnings ?? [])" :key="i">{{ w }}</li>
          </ul>
        </div>

        <div class="target">
          <label>输出方式</label>
          <label v-for="(lb, m) in modeLabel" :key="m" class="radio">
            <input type="radio" :value="m" :checked="mode === m" @change="setMode(m as any)" />
            <span>{{ lb }}</span>
          </label>
          <div v-if="mode !== 'overwrite'" class="outpath">
            <input v-model="outputPath" class="text" readonly :placeholder="mode === 'file' ? '目标文件路径…' : '目标父目录…'" />
            <button class="btn" @click="pickOutput">{{ mode === 'file' ? '另存为…' : '选择目录…' }}</button>
          </div>
        </div>

        <button class="btn primary big" :disabled="!canRollback || loading" @click="doRollback">
          执行回退
        </button>

        <div v-if="result && result.ok" class="result ok card">
          <h4>✅ {{ result.message }}</h4>
          <p v-if="result.backup_path" class="backup">原档备份：<code>{{ result.backup_path }}</code></p>
          <p v-if="result.output" class="output">输出：<code>{{ result.output }}</code></p>
          <ul v-if="result.warnings?.length" class="warnings">
            <li v-for="(w, i) in (result.warnings ?? [])" :key="i">{{ w }}</li>
          </ul>
        </div>
        <div v-if="result && !result.ok" class="result err card">{{ result.message }}</div>
      </aside>
    </main>

    <footer v-if="!info" class="empty card">
      <p>打开一个存档文件后，这里会列出全部幕/格进度（类型、各玩家 HP/金币）。</p>
      <p>已知限制：卡组/遗物/药水不回退；RNG 会重roll；跨幕回退暂不支持（visited_map_coords 只保留当前幕）。</p>
    </footer>
    <footer v-else class="foot">
      选中要重打的格 → 预览 → 选择输出方式 → 执行回退（回退后从所选格重新开始）。跨幕回退暂不支持；卡组/遗物/药水不回退；RNG 会重roll。
    </footer>
  </div>
</template>

<style>
:root {
  --bg: #0e101a;
  --bg2: #161a29;
  --card: #1b2033;
  --line: #2a3150;
  --fg: #e8eaf2;
  --dim: #9aa3c0;
  --accent: #e8b64c;
  --accent2: #d98e3a;
  --ok: #59c98f;
  --err: #e06c6c;
  --sel: #2c3a63;
  color-scheme: dark;
}
* { box-sizing: border-box; }
html, body { margin: 0; padding: 0; }
body {
  background: var(--bg);
  color: var(--fg);
  font-family: "Segoe UI", "Microsoft YaHei", system-ui, sans-serif;
  font-size: 14px;
}
.shell { max-width: 1280px; margin: 0 auto; padding: 18px 22px 40px; }
.topbar { display: flex; align-items: center; justify-content: space-between; margin-bottom: 14px; }
.brand { display: flex; gap: 12px; align-items: center; }
.logo { font-size: 30px; }
.brand h1 { margin: 0; font-size: 20px; }
.brand p { margin: 2px 0 0; color: var(--dim); font-size: 12px; }
.card { background: var(--card); border: 1px solid var(--line); border-radius: 10px; padding: 12px 14px; }
.error.card { background: #3a1d28; border-color: #6d3a4a; color: #ffb3b3; }
.filebar { display: flex; gap: 8px; margin-bottom: 12px; }
.filebar .text { flex: 1 1 auto; min-width: 280px; }
.tabs { display: flex; gap: 6px; margin: 0 0 12px; }
.tab {
  background: var(--bg2); color: var(--dim); border: 1px solid var(--line);
  border-radius: 8px 8px 0 0; padding: 8px 18px; cursor: pointer; font-size: 14px;
}
.tab:hover { color: var(--fg); }
.tab.on { background: var(--card); color: var(--accent); border-color: var(--accent); border-bottom-color: var(--card); font-weight: 600; }
.lock { color: var(--dim); font-size: 12px; font-weight: 400; margin-left: 8px; }
.text {
  flex: 1; background: var(--bg2); color: var(--fg); border: 1px solid var(--line);
  border-radius: 7px; padding: 7px 10px; font-size: 13px; min-width: 0;
}
.text:focus { outline: 1px solid var(--accent); }
.btn {
  background: var(--bg2); color: var(--fg); border: 1px solid var(--line);
  border-radius: 7px; padding: 7px 14px; cursor: pointer; font-size: 13px; white-space: nowrap;
}
.btn:hover:not(:disabled) { border-color: var(--accent); color: var(--accent); }
.btn:disabled { opacity: .45; cursor: not-allowed; }
.btn.primary { background: linear-gradient(135deg, var(--accent), var(--accent2)); color: #241a05; border: none; font-weight: 600; }
.btn.primary:hover:not(:disabled) { filter: brightness(1.08); color: #241a05; }
.btn.big { width: 100%; padding: 10px; font-size: 15px; }
.meta { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; margin-bottom: 12px; }
.chip { background: var(--bg2); border: 1px solid var(--line); border-radius: 20px; padding: 4px 12px; font-size: 12px; }
.players { display: flex; flex-wrap: wrap; gap: 6px; margin-left: auto; }
.player { color: var(--accent); border-color: #4a4020; }
.pdetails { margin-bottom: 12px; }
.pdetails h2 { margin: 0 0 10px; font-size: 16px; }
.pcols { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 14px; }
.pcol { border: 1px solid var(--line); border-radius: 8px; padding: 10px 12px; background: var(--bg2); }
.pcol h3 { margin: 0 0 8px; font-size: 14px; display: flex; align-items: baseline; gap: 8px; }
.phpmax { color: var(--dim); font-size: 12px; font-weight: 400; }
.group { font-size: 13px; }
.group summary { cursor: pointer; color: var(--accent); padding: 4px 0; user-select: none; }
.group summary:hover { color: #f4cd6f; }
.items { margin: 2px 0 8px; padding-left: 18px; color: var(--fg); }
.items li { margin: 2px 0; }
.items em { color: var(--dim); font-size: 11px; margin-left: 6px; font-style: normal; }
.cards { display: flex; flex-wrap: wrap; gap: 6px; margin: 6px 0 8px; }
.card {
  background: var(--bg); border: 1px solid var(--line); border-radius: 6px;
  padding: 3px 8px; font-size: 12px; display: inline-flex; align-items: baseline; gap: 5px;
}
.card.up { border-color: var(--accent); color: var(--accent2); }
.card i { color: var(--dim); font-size: 10px; font-style: normal; }
.cols { display: grid; grid-template-columns: minmax(0, 1.4fr) minmax(340px, 1fr); gap: 14px; align-items: start; }
.floors h2, .panel h2 { margin: 0 0 12px; font-size: 16px; }
.act { margin-bottom: 16px; }
.act h3 { margin: 8px 0 8px; font-size: 13px; color: var(--dim); font-weight: 600; }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 8px; }
.floor {
  position: relative; text-align: left; background: var(--bg2); color: var(--fg);
  border: 1px solid var(--line); border-radius: 8px; padding: 8px 10px; cursor: pointer;
  display: flex; flex-direction: column; gap: 3px;
}
.floor:hover { border-color: var(--accent); transform: translateY(-1px); }
.floor:disabled { cursor: not-allowed; }
.floor.locked { opacity: .42; }
.floor.locked:hover, .floor:disabled:hover { border-color: var(--line); transform: none; }
.floor.sel { background: var(--sel); border-color: var(--accent); }
.floor.last { border-color: #4a6b5e; }
.floor .idx { font-weight: 700; color: var(--accent); font-size: 13px; }
.floor .type { font-size: 13px; }
.floor .stat { font-size: 11px; color: var(--dim); }
.floor .badge { position: absolute; top: 6px; right: 8px; font-size: 10px; color: var(--ok); }
.hint { color: var(--dim); font-size: 12px; margin: 4px 0 0; }
.panel { position: sticky; top: 14px; display: flex; flex-direction: column; gap: 12px; }
.target { display: flex; flex-direction: column; gap: 6px; }
.target > label { font-size: 12px; color: var(--dim); }
.target-val { font-size: 14px; }
.target-val .type { color: var(--accent); margin-left: 8px; }
.tip { color: var(--dim); font-size: 12px; margin-left: 8px; }
.muted { color: var(--dim); font-size: 13px; }
.radio { display: flex; gap: 7px; align-items: center; font-size: 13px; padding: 3px 0; cursor: pointer; }
.outpath { display: flex; gap: 8px; margin-top: 6px; }
.preview { border: 1px solid var(--line); border-radius: 8px; padding: 10px 12px; background: var(--bg2); }
.preview-grid { display: grid; grid-template-columns: auto 1fr; gap: 6px 14px; font-size: 13px; }
.preview-grid > span { color: var(--dim); }
.stats { display: flex; flex-direction: column; gap: 2px; }
.warnings { margin: 10px 0 0; padding-left: 18px; color: #d8b36a; font-size: 12px; }
.warnings li { margin-bottom: 3px; }
.result { margin-top: 4px; }
.result.ok { border-color: #3d5d4e; }
.result.err { border-color: #6d3a4a; color: #ffb3b3; }
.result h4 { margin: 0 0 6px; font-size: 13px; }
.result p { margin: 3px 0; font-size: 12px; color: var(--dim); word-break: break-all; }
.result code { color: var(--fg); }
.empty { color: var(--dim); line-height: 1.7; }
.foot { color: var(--dim); font-size: 12px; text-align: center; margin-top: 14px; }
</style>