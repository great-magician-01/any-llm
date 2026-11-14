<script setup lang="ts">
import { ref, computed, onMounted, h, type VNode } from 'vue'
import {
  useMessage,
  NPopconfirm,
  NButton,
  NTag,
  NSpace,
  NModal,
  NCard,
  NForm,
  NFormItem,
  NInput,
  NSelect,
  NTooltip,
  NRadioGroup,
  NRadioButton,
  NTabs,
  NTabPane,
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { listAliases, createAlias, updateAlias, deleteAlias, activateAliasProfile, type ModelAlias } from '@/api/aliases'
import { listUpstreams, listModels, type Upstream, type UpstreamModel } from '@/api/upstreams'
import AppIcon from '@/components/AppIcon.vue'
import { aliasUpstreamOptions } from '@/utils/aliasOptions'
import { isExpired } from '@/utils/upstreamStatus'
import { formatTime } from '@/utils/format'
import { errText } from '@/utils/errText'
import {
  DEFAULT_ALIAS_PROFILE,
  flattenAliasProfiles,
  groupBindingsByProfile,
  normalizeProfileName,
  profileLabel,
  resolveActiveProfile,
  uniqueProfileName,
  type ProfileForm,
} from '@/utils/aliasProfiles'

const message = useMessage()
const aliases = ref<ModelAlias[]>([])
const upstreams = ref<Upstream[]>([])

// 编辑弹窗：profiles 是正在编辑的多套配置，activeTab 只是当前查看的页签，
// 与“当前生效配置”（form.activeProfile）相互独立。
const showModal = ref(false)
const editing = ref<ModelAlias | null>(null)
interface BindingRow { upstream_id: number | null; model_name: string }
const form = ref<{ name: string; activeProfile: string; profiles: ProfileForm[] }>({
  name: '',
  activeProfile: DEFAULT_ALIAS_PROFILE,
  profiles: [],
})
const activeTab = ref(0)

// 列表页一键切换中的别名 id：切换是异步写库，期间禁用该行的切换控件。
const switching = ref<number | null>(null)

// 模型下拉选项按上游缓存，选中上游时按需加载
const modelOptions = ref<Record<number, UpstreamModel[]>>({})

function allFormUpstreamIDs(): number[] {
  return form.value.profiles
    .flatMap((p) => p.bindings.map((b) => b.upstream_id))
    .filter((id): id is number => id != null)
}

// 禁用的上游不进下拉；但编辑时已指向禁用上游的旧绑定要能显示上游名，
// 所以这些上游补成不可选项保留（见 aliasUpstreamOptions）。
const upstreamOptions = computed(() => aliasUpstreamOptions(upstreams.value, allFormUpstreamIDs()))

// 绑定行的上游是否已过有效期：绑定只带 upstream_enabled 快照，有效期要从上游
// 列表现查（与网关的读时判定一致，见 utils/upstreamStatus）。
function isExpiredUpstream(id: number) {
  const u = upstreams.value.find((x) => x.id === id)
  return u ? isExpired(u) : false
}

function modelOptionsFor(upstreamId: number | null) {
  if (!upstreamId) return []
  return (modelOptions.value[upstreamId] || []).map((m) => ({ label: m.model_name, value: m.model_name }))
}

async function ensureModelOptions(upstreamId: number | null) {
  if (!upstreamId || modelOptions.value[upstreamId]) return
  try {
    const ms = await listModels(upstreamId)
    modelOptions.value = { ...modelOptions.value, [upstreamId]: ms }
  } catch {
    // 模型列表拉取失败不阻塞：输入框支持手输任意模型名
  }
}

// ---------------------------------------------------------------------------
// 列表展示 / 一键切换
// ---------------------------------------------------------------------------

function profileNamesOf(row: ModelAlias): string[] {
  return groupBindingsByProfile(row.bindings || []).map((g) => g.name)
}

function activeBindingsOf(row: ModelAlias) {
  const active = normalizeProfileName(row.active_profile || '')
  return (row.bindings || []).filter((b) => normalizeProfileName(b.profile || '') === active)
}

async function switchProfile(row: ModelAlias, profile: string) {
  if (profile === normalizeProfileName(row.active_profile || '') || switching.value != null) return
  switching.value = row.id
  try {
    const updated = await activateAliasProfile(row.id, profile)
    const i = aliases.value.findIndex((a) => a.id === row.id)
    if (i >= 0) aliases.value[i] = updated
    message.success(`已切换到「${profileLabel(profile)}」配置`)
  } catch (e) {
    message.error('切换配置失败：' + errText(e))
  } finally {
    switching.value = null
  }
}

const columns = computed<DataTableColumns<ModelAlias>>(() => [
  {
    title: '对外模型名',
    key: 'name',
    render: (row) => h('code', { class: 'mono alias-chip' }, row.name),
  },
  {
    title: '当前配置的绑定链（按故障转移顺序）',
    key: 'bindings',
    render(row) {
      const bindings = activeBindingsOf(row)
      const nodes: VNode[] = [
        h(
          NTag,
          { size: 'small', bordered: false, type: 'info' },
          { default: () => `当前：${profileLabel(normalizeProfileName(row.active_profile || ''))}` },
        ),
      ]
      if (!bindings.length) {
        nodes.push(h('span', { style: 'color: var(--text-4)' }, '该配置暂无可用绑定'))
        return h('div', { style: 'display: flex; align-items: center; gap: 6px; flex-wrap: wrap' }, nodes)
      }
      bindings.forEach((b, i) => {
        if (i > 0) nodes.push(h(AppIcon, { name: 'arrow', size: 12, style: 'color: var(--text-4); flex: none' }))
        const dead = !b.upstream_name
        // 上游被禁用（而非删除）：名字仍在，但网关解析时会跳过该绑定
        const off = !!b.upstream_name && b.upstream_enabled === false
        // 已过有效期同样被网关跳过（与禁用同级的提示，避免绿标误导排障）
        const expired = !dead && !off && isExpiredUpstream(b.upstream_id)
        const tip = dead
          ? '该绑定指向的上游已被删除，请求时会跳过'
          : off
            ? '该绑定指向的上游已被禁用，请求时会跳过'
            : expired
              ? '该绑定指向的上游已过有效期，请求时会跳过（续期后恢复）'
              : i === 0
                ? '首选绑定'
                : `第 ${i + 1} 顺位（前面全部失败时兜底）`
        nodes.push(
          h(
            NTooltip,
            { trigger: 'hover' },
            {
              trigger: () =>
                h(
                  NTag,
                  { size: 'small', bordered: false, type: dead ? 'error' : off || expired ? 'warning' : i === 0 ? 'success' : 'default' },
                  { default: () => `${b.upstream_name || '上游#' + b.upstream_id} / ${b.model_name}` },
                ),
              default: () => tip,
            },
          ),
        )
      })
      return h('div', { style: 'display: flex; align-items: center; gap: 6px; flex-wrap: wrap' }, nodes)
    },
  },
  {
    title: '一键切换配置',
    key: 'profiles',
    width: 220,
    render(row) {
      const names = profileNamesOf(row)
      if (names.length <= 1) {
        return h(NTag, { size: 'small', bordered: false }, { default: () => profileLabel(names[0] || DEFAULT_ALIAS_PROFILE) })
      }
      return h(
        NRadioGroup,
        {
          value: normalizeProfileName(row.active_profile || ''),
          size: 'small',
          disabled: switching.value === row.id,
          style: 'display: flex; flex-wrap: wrap; gap: 4px',
          'onUpdate:value': (v: string) => { void switchProfile(row, v) },
        },
        { default: () => names.map((n) => h(NRadioButton, { key: n, value: n }, { default: () => profileLabel(n) })) },
      )
    },
  },
  {
    title: '创建时间',
    key: 'created_at',
    width: 170,
    render: (row) => h('span', { style: 'color: var(--text-3); font-size: 12px' }, formatTime(row.created_at || '')),
  },
  {
    title: '操作',
    key: 'actions',
    width: 150,
    render(row) {
      return h(NSpace, { size: 4 }, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => openEdit(row) }, { default: () => '编辑' }),
          h(
            NPopconfirm,
            { onPositiveClick: () => { del(row.id) } },
            {
              trigger: () => h(NButton, { size: 'small', type: 'error', quaternary: true }, { default: () => '删除' }),
              default: () => '确定删除此别名？删除后使用该别名的请求将失败。',
            },
          ),
        ],
      })
    },
  },
])

// ---------------------------------------------------------------------------
// 编辑弹窗
// ---------------------------------------------------------------------------

function blankBinding(): BindingRow {
  return { upstream_id: null, model_name: '' }
}

function openCreate() {
  editing.value = null
  form.value = {
    name: '',
    activeProfile: DEFAULT_ALIAS_PROFILE,
    profiles: [{ name: DEFAULT_ALIAS_PROFILE, bindings: [blankBinding()] }],
  }
  activeTab.value = 0
  showModal.value = true
}

function openEdit(row: ModelAlias) {
  editing.value = row
  const groups = groupBindingsByProfile(row.bindings || [])
  const profiles: ProfileForm[] = groups.length
    ? groups.map((g) => ({
        name: g.name,
        bindings: g.bindings.map((b) => ({ upstream_id: b.upstream_id, model_name: b.model_name })),
      }))
    : [{ name: DEFAULT_ALIAS_PROFILE, bindings: [blankBinding()] }]
  form.value = {
    name: row.name,
    activeProfile: resolveActiveProfile(normalizeProfileName(row.active_profile || ''), profiles),
    profiles,
  }
  activeTab.value = 0
  for (const p of profiles) for (const b of p.bindings) ensureModelOptions(b.upstream_id)
  showModal.value = true
}

const activeProfileOptions = computed(() =>
  form.value.profiles.map((p) => {
    const name = normalizeProfileName(p.name)
    return { label: profileLabel(name), value: name }
  }),
)

function addBinding(p: ProfileForm) {
  p.bindings.push(blankBinding())
}

function removeBinding(p: ProfileForm, i: number) {
  p.bindings.splice(i, 1)
}

function moveBinding(p: ProfileForm, i: number, dir: -1 | 1) {
  const j = i + dir
  if (j < 0 || j >= p.bindings.length) return
  const arr = p.bindings
  ;[arr[i], arr[j]] = [arr[j], arr[i]]
}

function addProfile() {
  const names = form.value.profiles.map((p) => normalizeProfileName(p.name))
  form.value.profiles.push({ name: uniqueProfileName(names), bindings: [blankBinding()] })
  activeTab.value = form.value.profiles.length - 1
}

function removeProfile(i: number) {
  if (form.value.profiles.length <= 1) return
  const removed = normalizeProfileName(form.value.profiles[i].name)
  form.value.profiles.splice(i, 1)
  if (normalizeProfileName(form.value.activeProfile) === removed) {
    form.value.activeProfile = normalizeProfileName(form.value.profiles[0].name)
  }
  if (activeTab.value >= form.value.profiles.length) activeTab.value = form.value.profiles.length - 1
  else if (activeTab.value > i) activeTab.value--
}

function renameProfile(p: ProfileForm, value: string) {
  const wasActive = normalizeProfileName(form.value.activeProfile) === normalizeProfileName(p.name)
  p.name = value
  if (wasActive) form.value.activeProfile = normalizeProfileName(value)
}

// 保存前把表单归一成「非空且唯一的配置名 + 每条绑定完整」；返回 error 时
// 不发起请求。配置名允许任意字符，长度上限与后端（64 字符）一致。
function normalizeProfilesForSave(): { profiles: ProfileForm[]; error: string } {
  const profiles: ProfileForm[] = []
  const seen = new Set<string>()
  for (const p of form.value.profiles) {
    const name = normalizeProfileName(p.name)
    if ([...name].length > 64) {
      return { profiles: [], error: `配置名「${name}」过长（最多 64 个字符）` }
    }
    if (seen.has(name)) {
      return { profiles: [], error: `配置名「${name}」重复，请改成不同的名字` }
    }
    seen.add(name)
    const bindings = p.bindings.filter((b) => b.upstream_id != null || b.model_name.trim())
    if (bindings.length === 0) {
      return { profiles: [], error: `配置「${profileLabel(name)}」还没有绑定` }
    }
    for (let i = 0; i < bindings.length; i++) {
      if (!bindings[i].upstream_id) {
        return { profiles: [], error: `配置「${profileLabel(name)}」第 ${i + 1} 条绑定未选择上游` }
      }
      if (!bindings[i].model_name.trim()) {
        return { profiles: [], error: `配置「${profileLabel(name)}」第 ${i + 1} 条绑定未填写模型名` }
      }
    }
    profiles.push({ name, bindings })
  }
  if (profiles.length === 0) {
    return { profiles: [], error: '请至少添加一套配置' }
  }
  return { profiles, error: '' }
}

async function save() {
  const name = form.value.name.trim()
  if (!name) {
    message.warning('请填写对外模型名')
    return
  }
  const { profiles, error } = normalizeProfilesForSave()
  if (error) {
    message.warning(error)
    return
  }
  const bindings = flattenAliasProfiles(profiles)
  const activeProfile = resolveActiveProfile(normalizeProfileName(form.value.activeProfile), profiles)
  try {
    if (editing.value) {
      await updateAlias(editing.value.id, { name, active_profile: activeProfile, bindings })
      message.success('已保存')
    } else {
      await createAlias(name, activeProfile, bindings)
      message.success('已创建')
    }
    showModal.value = false
    editing.value = null
    await load()
  } catch (e) {
    message.error('保存失败：' + errText(e))
  }
}

async function load() {
  try {
    const [as, us] = await Promise.all([listAliases(), listUpstreams()])
    aliases.value = as
    upstreams.value = us
  } catch (e) {
    message.error('加载别名失败：' + errText(e))
  }
}

async function del(id: number) {
  try {
    await deleteAlias(id)
    await load()
    message.success('已删除')
  } catch {
    message.error('删除失败')
  }
}

onMounted(load)
</script>

<template>
  <div>
    <header class="page-header">
      <div>
        <h1>模型别名</h1>
        <p>
          固定对外模型名：客户端直接请求别名（无需 <code class="mono code-chip">upstream/model</code> 格式），
          网关按绑定顺序尝试，失败自动切换到下一个绑定。一个别名可存多套配置，列表页可一键切换当前生效配置
        </p>
      </div>
      <div class="page-header-side">
        <n-button quaternary circle @click="load">
          <template #icon><AppIcon name="refresh" :size="16" /></template>
        </n-button>
      </div>
    </header>

    <n-card title="别名列表" class="panel">
      <template #header-extra>
        <n-button type="primary" size="small" @click="openCreate">
          <template #icon><AppIcon name="plus" :size="14" /></template>
          新增别名
        </n-button>
      </template>
      <n-data-table :bordered="false" :columns="columns" :data="aliases" />
    </n-card>

    <n-modal :show="showModal" @update:show="(s: boolean) => { showModal = s }">
      <n-card :title="editing ? '编辑别名' : '新增别名'" :bordered="false" style="width:760px" role="dialog" aria-modal="true">
        <n-form label-placement="top">
          <n-form-item label="对外模型名">
            <n-input v-model:value="form.name" placeholder="客户端请求时使用的模型名，如：claude-sonnet 或 my-gpt" />
          </n-form-item>
          <n-form-item label="多套配置（每套是一条独立绑定链，保存后可在列表页一键切换）">
            <div class="profiles">
              <n-form-item label="当前生效配置" :show-feedback="false">
                <n-select v-model:value="form.activeProfile" :options="activeProfileOptions" style="width: 220px" />
              </n-form-item>
              <n-tabs v-model:value="activeTab" type="card" animated>
                <n-tab-pane
                  v-for="(p, pi) in form.profiles"
                  :key="pi"
                  :name="pi"
                  :tab="profileLabel(normalizeProfileName(p.name))"
                >
                  <div class="profile-tools">
                    <n-input
                      :value="p.name"
                      placeholder="配置名，如：峰期 / 谷期 / 节假日"
                      @update:value="(v: string) => renameProfile(p, v)"
                    />
                    <n-button
                      size="small"
                      quaternary
                      type="error"
                      :disabled="form.profiles.length <= 1"
                      @click="removeProfile(pi)"
                    >
                      删除此配置
                    </n-button>
                  </div>
                  <div class="binding-list">
                    <div v-for="(b, i) in p.bindings" :key="i" class="binding-row">
                      <span class="binding-order mono">{{ i + 1 }}</span>
                      <n-select
                        :value="b.upstream_id"
                        :options="upstreamOptions"
                        placeholder="选择上游"
                        style="width: 220px"
                        @update:value="(v: number | null) => { b.upstream_id = v; ensureModelOptions(v) }"
                      />
                      <n-select
                        v-model:value="b.model_name"
                        :options="modelOptionsFor(b.upstream_id)"
                        placeholder="模型名（可手输）"
                        filterable
                        tag
                        style="flex: 1"
                      />
                      <n-button quaternary circle size="small" :disabled="i === 0" @click="moveBinding(p, i, -1)">
                        <template #icon><span class="mono">↑</span></template>
                      </n-button>
                      <n-button quaternary circle size="small" :disabled="i === p.bindings.length - 1" @click="moveBinding(p, i, 1)">
                        <template #icon><span class="mono">↓</span></template>
                      </n-button>
                      <n-button quaternary circle size="small" type="error" :disabled="p.bindings.length <= 1" @click="removeBinding(p, i)">
                        <template #icon><span class="mono">×</span></template>
                      </n-button>
                    </div>
                    <n-button dashed block size="small" @click="addBinding(p)">
                      <template #icon><AppIcon name="plus" :size="13" /></template>
                      添加绑定（故障转移兜底）
                    </n-button>
                  </div>
                </n-tab-pane>
              </n-tabs>
              <n-button dashed block size="small" @click="addProfile">
                <template #icon><AppIcon name="plus" :size="13" /></template>
                添加配置（如：峰期 / 谷期 / 节假日）
              </n-button>
            </div>
          </n-form-item>
        </n-form>
        <template #footer>
          <div style="text-align: right">
            <n-button style="margin-right: 8px" @click="showModal = false">取消</n-button>
            <n-button type="primary" @click="save">保存</n-button>
          </div>
        </template>
      </n-card>
    </n-modal>
  </div>
</template>

<style scoped>
.alias-chip {
  padding: 3px 8px;
  border-radius: 6px;
  background: var(--overlay-1);
  border: 1px solid var(--border-soft);
  font-size: 12px;
  color: var(--text-2);
}
.code-chip {
  padding: 1px 6px;
  border-radius: 5px;
  background: var(--overlay-2);
  color: var(--text-2);
  font-size: 12px;
}
.profiles {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.profile-tools {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}
.profile-tools .n-input {
  flex: 1;
}
.binding-list {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.binding-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.binding-order {
  flex: none;
  width: 18px;
  text-align: center;
  color: var(--text-3);
  font-size: 12px;
}
</style>
