import { ref, type Ref } from 'vue'
import { useMessage } from 'naive-ui'
import { createKey, updateKey, type ExtKey } from '../api/keys'

// 两套 UI 皮肤（views/Keys.vue 与 glass/views/GlassKeys.vue）共用的密钥表单
// 状态与保存逻辑——规则只维护这一份，不要复制回页面。

export interface KeyCreateForm {
  label: string
  remark: string
  daily_token_limit: number
  monthly_token_limit: number
  allowed_models: string[]
}

export interface KeyEditForm extends KeyCreateForm {
  enabled: boolean
}

export function emptyKeyCreateForm(): KeyCreateForm {
  return { label: '', remark: '', daily_token_limit: 0, monthly_token_limit: 0, allowed_models: [] }
}

// 名称唯一的前端即时提示（以服务端判定为准）：空名不参与；两侧都 trim 后比较，
// 与服务端 CreateExtKey/UpdateExtKey 的 TrimSpace 归一口径一致。
export function keyLabelTaken(keys: ExtKey[], label: string, exceptID?: number): boolean {
  if (!label) return false
  return keys.some((k) => k.label.trim() === label && k.id !== exceptID)
}

export function useKeyForms(keys: Ref<ExtKey[]>, opts: {
  reload: () => Promise<void>
  ensureModelOptions: () => void
}) {
  const message = useMessage()

  // create modal: 'form' = filling form, 'done' = showing generated key
  const showCreateModal = ref(false)
  const createModalState = ref<'form' | 'done'>('form')
  const createForm = ref<KeyCreateForm>(emptyKeyCreateForm())
  const newlyCreatedKey = ref('')

  // edit modal
  const showEditModal = ref(false)
  const editing = ref<ExtKey | null>(null)
  const editForm = ref<KeyEditForm>({ ...emptyKeyCreateForm(), enabled: true })

  function openCreate() {
    createForm.value = emptyKeyCreateForm()
    newlyCreatedKey.value = ''
    createModalState.value = 'form'
    showCreateModal.value = true
    opts.ensureModelOptions()
  }

  function resetCreateForm() {
    openCreate()
  }

  async function saveCreate() {
    const label = createForm.value.label.trim()
    if (!label) {
      message.warning('请填写名称')
      return
    }
    if (keyLabelTaken(keys.value, label)) {
      message.warning('名称已存在，请换一个')
      return
    }
    try {
      const k = await createKey(label, createForm.value.remark.trim(), createForm.value.daily_token_limit, createForm.value.monthly_token_limit, createForm.value.allowed_models)
      newlyCreatedKey.value = k.key
      createModalState.value = 'done'
      await opts.reload()
    } catch (e: any) {
      message.error('创建失败：' + (e?.response?.data?.error || e?.message || String(e)))
    }
  }

  function openEdit(row: ExtKey) {
    editing.value = row
    editForm.value = {
      label: row.label,
      remark: row.remark,
      enabled: row.enabled,
      daily_token_limit: row.daily_token_limit,
      monthly_token_limit: row.monthly_token_limit,
      allowed_models: row.allowed_models ?? [],
    }
    showEditModal.value = true
    opts.ensureModelOptions()
  }

  async function saveEdit() {
    if (!editing.value) return
    const cur = editing.value
    const label = editForm.value.label.trim()
    if (!label) {
      message.warning('请填写名称')
      return
    }
    // 只在真正改名时提示重名：老数据可能本就重名（唯一性约束是后加的），
    // 不改名的保存不能被别人的重名卡住；改名冲突最终以服务端判定为准。
    if (label !== cur.label.trim() && keyLabelTaken(keys.value, label, cur.id)) {
      message.warning('名称已存在，请换一个')
      return
    }
    try {
      await updateKey(cur.id, {
        label,
        remark: editForm.value.remark.trim(),
        enabled: editForm.value.enabled,
        daily_token_limit: editForm.value.daily_token_limit,
        monthly_token_limit: editForm.value.monthly_token_limit,
        allowed_models: editForm.value.allowed_models,
      })
      showEditModal.value = false
      editing.value = null
      await opts.reload()
      message.success('已保存')
    } catch (e: any) {
      message.error('保存失败：' + (e?.response?.data?.error || e?.message || String(e)))
    }
  }

  return {
    showCreateModal, createModalState, createForm, newlyCreatedKey,
    showEditModal, editing, editForm,
    openCreate, saveCreate, resetCreateForm, openEdit, saveEdit,
  }
}
