package adminapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/store"
)

// 配置导出/导入：上游（含真实 API Key、模型列表、额度）与模型别名。别名
// 绑定按上游名引用（而非实例内的自增 ID），保证导出文件跨实例可用。
//
// 导入语义：同名覆盖——上游全字段覆盖、模型列表精确替换；别名绑定整体替换。
// 文件里没出现的现有配置原样保留。字段级例外：enabled / 两个 token 上限 /
// 并发上限 / expires_at / remark / tag / models / extra_endpoints 在文件里缺省时
// 保留现状（导入侧用指针/nil 区分「没给」，expires_at 另需区分「显式 null =
// 清除有效期」，extra_endpoints 显式空数组 = 清空，tag 显式空串 = 回默认官方），
// 其余字段一律以文件为准。
// 缺省保留的附加端点会与文件给的新主格式做合并撞车校验（与库中行冲突即整体
// 400，见 handleConfigImport 的预检）。绑定指向的上游在文件与库中都不存在时
// 丢弃该绑定（计数 bindings_dropped）；一个可解析绑定都不剩的别名整个跳过。
// 文件内重名条目（上游/别名/单上游内模型）整体 400 拒绝。导入非单事务：
// 中途 DB 失败时已提交的条目会保留，直接重导同一文件即可收敛（同名覆盖幂等）。

const configVersion = 1

type configUpstreamModel struct {
	ModelName       string `json:"model_name"`
	Manual          bool   `json:"manual"`
	ContextLength   int    `json:"context_length"`
	MaxOutputLength int    `json:"max_output_length"`
	// 旧版导出文件没有该字段：导入侧按 bool 零值落到「否」，与新建默认一致。
	Multimodal bool `json:"multimodal"`
}

type configUpstream struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Format  string `json:"format"`
	// 附加格式端点（选填）。指针沿用「缺席即保留」惯例：导入时 nil = 新建不带 /
	// 更新保留现值，显式空数组 = 清空；导出侧无条件写出（空也落成 []），否则
	// 导出→导入往返永远无法清空该字段。version 保持 1：旧文件没有该字段，语义不变。
	ExtraEndpoints *[]store.UpstreamEndpoint `json:"extra_endpoints,omitempty"`
	Remark         *string                   `json:"remark"`
	// 上游标记（官方/中转）。与 remark 同款指针三态：导入时 nil = 新建按默认
	// 官方 / 更新保留现值，显式空串 = 回官方，非法值整体 400；导出侧无条件写出
	// （与 extra_endpoints 同理——缺省在导入侧是「保留」，不写该键的导出文件
	// 永远改不了目标实例上的标记）。version 保持 1：旧文件没有该键，语义不变。
	Tag               *string               `json:"tag"`
	Enabled           *bool                 `json:"enabled"`
	ExpiresAt         optTime               `json:"expires_at"`
	DailyTokenLimit   *int                  `json:"daily_token_limit"`
	MonthlyTokenLimit *int                  `json:"monthly_token_limit"`
	MaxConcurrent     *int                  `json:"max_concurrent"`
	Models            []configUpstreamModel `json:"models"`
}

type configBinding struct {
	Upstream  string `json:"upstream"`
	ModelName string `json:"model_name"`
}

type configAlias struct {
	Name     string          `json:"name"`
	Bindings []configBinding `json:"bindings"`
}

// configFile 同时充当导出响应与导入请求体（导入侧忽略 exported_at）。
type configFile struct {
	Version    int              `json:"version"`
	ExportedAt time.Time        `json:"exported_at"`
	Upstreams  []configUpstream `json:"upstreams"`
	Aliases    []configAlias    `json:"aliases"`
}

type importResult struct {
	UpstreamsCreated int `json:"upstreams_created"`
	UpstreamsUpdated int `json:"upstreams_updated"`
	AliasesCreated   int `json:"aliases_created"`
	AliasesUpdated   int `json:"aliases_updated"`
	AliasesSkipped   int `json:"aliases_skipped"`
	BindingsDropped  int `json:"bindings_dropped"`
}

func (a *API) handleConfigExport(w http.ResponseWriter, r *http.Request) {
	ups, err := store.ListUpstreams(a.db, nil)
	if err != nil {
		logger.Error("admin: export config list upstreams failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	aliases, err := store.ListAliases(a.db)
	if err != nil {
		logger.Error("admin: export config list aliases failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	out := configFile{Version: configVersion, ExportedAt: time.Now(),
		Upstreams: make([]configUpstream, 0, len(ups)), Aliases: make([]configAlias, 0, len(aliases))}
	for _, u := range ups {
		cu := configUpstream{Name: u.Name, BaseURL: u.BaseURL, APIKey: u.APIKey, Format: u.Format, Remark: &u.Remark,
			Tag:             &u.Tag,
			DailyTokenLimit: &u.DailyTokenLimit, MonthlyTokenLimit: &u.MonthlyTokenLimit, MaxConcurrent: &u.MaxConcurrent,
			ExpiresAt: optTime{set: true, t: u.ExpiresAt}}
		// 附加端点无条件写出（没有也落成 []）：缺省在导入侧是「保留现值」，
		// 不写该键的导出文件永远无法清空目标实例上的附加端点（有损往返）。
		eps := append([]store.UpstreamEndpoint{}, u.ExtraEndpoints...)
		cu.ExtraEndpoints = &eps
		en := u.Enabled
		cu.Enabled = &en
		models, err := store.ListModels(a.db, u.ID)
		if err != nil {
			logger.Error("admin: export config list models failed", "upstream", u.Name, "err", err)
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		cu.Models = make([]configUpstreamModel, 0, len(models))
		for _, m := range models {
			cu.Models = append(cu.Models, configUpstreamModel{ModelName: m.ModelName, Manual: m.Manual,
				ContextLength: m.ContextLength, MaxOutputLength: m.MaxOutputLength, Multimodal: m.Multimodal})
		}
		out.Upstreams = append(out.Upstreams, cu)
	}
	for _, al := range aliases {
		ca := configAlias{Name: al.Name, Bindings: make([]configBinding, 0, len(al.Bindings))}
		for _, b := range al.Bindings {
			// 指向已删除上游的绑定（管理端联查给出空名）在导出文件里无法
			// 解析回上游，跳过即可——正常路径 DeleteUpstream 已顺带软删绑定。
			if b.UpstreamName == "" {
				continue
			}
			ca.Bindings = append(ca.Bindings, configBinding{Upstream: b.UpstreamName, ModelName: b.ModelName})
		}
		out.Aliases = append(out.Aliases, ca)
	}
	writeJSON(w, 200, out)
}

func (a *API) handleConfigImport(w http.ResponseWriter, r *http.Request) {
	var in configFile
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		logger.Warn("admin: import config invalid JSON", "err", err)
		writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
		return
	}
	if in.Version > configVersion {
		writeJSON(w, 400, map[string]any{"error": fmt.Sprintf("unsupported config version %d (max %d)", in.Version, configVersion)})
		return
	}
	// 全量校验放在任何写入之前：文件不合法就整体拒绝，不留半导入状态。
	if err := validateImport(&in); err != nil {
		logger.Warn("admin: import config invalid payload", "err", err)
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	var res importResult
	if err := a.writeSync(func(d *sql.DB) error {
		// 合并口径预检：文件缺省 extra_endpoints = 保留库中现值，若保留的附加
		// 端点与文件给的新主格式撞车（同一格式两个 URL），合并结果是一行连
		// PATCH 禁用都会被拒的冲突配置。validateImport 是纯文件校验看不到库，
		// 必须在任何写入前对照现有行拒掉，守住「不合法就整体拒绝」。
		for i := range in.Upstreams {
			u := &in.Upstreams[i]
			if u.ExtraEndpoints != nil {
				continue // 附加端点以文件为准的组合已由 validateImport 校验
			}
			ex, err := store.GetUpstreamByName(d, u.Name)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return fmt.Errorf("get upstream %q: %w", u.Name, err)
			}
			if err := store.ValidateExtraEndpoints(u.Format, ex.ExtraEndpoints); err != nil {
				return fmt.Errorf("upstreams[%d] %q: retained extra_endpoints conflict with the file's format: %w", i, u.Name, err)
			}
		}
		// 先处理全部上游（建好 名→ID 映射），别名绑定再按名解析。
		ids := make(map[string]int64, len(in.Upstreams))
		for i := range in.Upstreams {
			u := &in.Upstreams[i]
			id, created, err := upsertUpstream(d, u)
			if err != nil {
				return err
			}
			ids[u.Name] = id
			if created {
				res.UpstreamsCreated++
			} else {
				res.UpstreamsUpdated++
			}
			if u.Models != nil {
				models := make([]store.UpstreamModel, 0, len(u.Models))
				for _, m := range u.Models {
					models = append(models, store.UpstreamModel{ModelName: m.ModelName, Manual: m.Manual,
						ContextLength: m.ContextLength, MaxOutputLength: m.MaxOutputLength, Multimodal: m.Multimodal})
				}
				if err := store.ReplaceModelsExact(d, id, models); err != nil {
					return fmt.Errorf("upstream %q models: %w", u.Name, err)
				}
			}
		}
		for i := range in.Aliases {
			al := &in.Aliases[i]
			bindings := make([]store.AliasBinding, 0, len(al.Bindings))
			for _, b := range al.Bindings {
				id, ok := ids[b.Upstream]
				if !ok {
					ex, err := store.GetUpstreamByName(d, b.Upstream)
					if errors.Is(err, sql.ErrNoRows) {
						// 文件与库中都无此上游：丢弃该绑定（计数可观测）
						res.BindingsDropped++
						continue
					}
					if err != nil {
						// 真实 DB 错误必须让导入失败，不能伪装成「不存在」
						return fmt.Errorf("resolve upstream %q: %w", b.Upstream, err)
					}
					id = ex.ID
				}
				bindings = append(bindings, store.AliasBinding{UpstreamID: id, ModelName: b.ModelName})
			}
			if len(bindings) == 0 {
				res.AliasesSkipped++
				continue
			}
			ex, err := store.GetAliasByName(d, al.Name)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("alias %q: %w", al.Name, err)
			}
			if err == nil {
				ex.Bindings = bindings
				if err := store.UpdateAlias(d, ex); err != nil {
					return fmt.Errorf("alias %q: %w", al.Name, err)
				}
				res.AliasesUpdated++
			} else if _, err := store.CreateAlias(d, &store.ModelAlias{Name: al.Name, Bindings: bindings}); err != nil {
				return fmt.Errorf("alias %q: %w", al.Name, err)
			} else {
				res.AliasesCreated++
			}
		}
		return nil
	}); err != nil {
		logger.Error("admin: import config DB write failed", "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	writeJSON(w, 200, res)
}

// validateImport 校验导入文件（空白裁剪后）。任何一项不合法都返回错误，
// 调用方整体拒绝、不做任何写入。
func validateImport(in *configFile) error {
	// 文件内重名（上游/别名）整体拒绝：静默后者覆盖前者会丢数据，手写
	// 文件场景宁可报错也不要悄悄丢弃前面的条目。
	seenUpstreams := make(map[string]bool, len(in.Upstreams))
	seenAliases := make(map[string]bool, len(in.Aliases))
	for i := range in.Upstreams {
		u := &in.Upstreams[i]
		u.Name = strings.TrimSpace(u.Name)
		if u.Name == "" {
			return fmt.Errorf("upstreams[%d]: name is required", i)
		}
		if seenUpstreams[u.Name] {
			return fmt.Errorf("upstreams[%d]: duplicate name %q", i, u.Name)
		}
		seenUpstreams[u.Name] = true
		if u.Format != "openai" && u.Format != "anthropic" && u.Format != "responses" {
			return fmt.Errorf("upstreams[%d]: format must be openai, anthropic or responses", i)
		}
		if u.ExtraEndpoints != nil {
			if err := store.ValidateExtraEndpoints(u.Format, *u.ExtraEndpoints); err != nil {
				return fmt.Errorf("upstreams[%d]: %w", i, err)
			}
		}
		if u.Tag != nil {
			if _, err := store.NormalizeTag(*u.Tag); err != nil {
				return fmt.Errorf("upstreams[%d]: %w", i, err)
			}
		}
		if u.DailyTokenLimit != nil && *u.DailyTokenLimit < 0 {
			return fmt.Errorf("upstreams[%d]: daily_token_limit must be >= 0", i)
		}
		if u.MonthlyTokenLimit != nil && *u.MonthlyTokenLimit < 0 {
			return fmt.Errorf("upstreams[%d]: monthly_token_limit must be >= 0", i)
		}
		if u.MaxConcurrent != nil && *u.MaxConcurrent < 0 {
			return fmt.Errorf("upstreams[%d]: max_concurrent must be >= 0 (0 = unlimited)", i)
		}
		seen := make(map[string]bool, len(u.Models))
		for j := range u.Models {
			m := &u.Models[j]
			m.ModelName = strings.TrimSpace(m.ModelName)
			if m.ModelName == "" {
				return fmt.Errorf("upstreams[%d].models[%d]: model_name is required", i, j)
			}
			if seen[m.ModelName] {
				return fmt.Errorf("upstreams[%d].models[%d]: duplicate model_name %q", i, j, m.ModelName)
			}
			seen[m.ModelName] = true
		}
	}
	for i := range in.Aliases {
		al := &in.Aliases[i]
		al.Name = strings.TrimSpace(al.Name)
		if al.Name == "" {
			return fmt.Errorf("aliases[%d]: name is required", i)
		}
		if seenAliases[al.Name] {
			return fmt.Errorf("aliases[%d]: duplicate name %q", i, al.Name)
		}
		seenAliases[al.Name] = true
		for j := range al.Bindings {
			b := &al.Bindings[j]
			b.Upstream = strings.TrimSpace(b.Upstream)
			b.ModelName = strings.TrimSpace(b.ModelName)
			if b.Upstream == "" || b.ModelName == "" {
				return fmt.Errorf("aliases[%d].bindings[%d]: upstream and model_name are required", i, j)
			}
		}
	}
	return nil
}

// upsertUpstream 按名覆盖或创建上游，返回（ID, 是否新建）。遵循
// UpdateUpstream「先 Get 再改再存」的约定；文件缺省的 enabled/限额保留现状。
func upsertUpstream(d *sql.DB, u *configUpstream) (int64, bool, error) {
	ex, err := store.GetUpstreamByName(d, u.Name)
	if errors.Is(err, sql.ErrNoRows) {
		nu := &store.Upstream{Name: u.Name, BaseURL: u.BaseURL, APIKey: u.APIKey, Format: u.Format, Enabled: true,
			MaxConcurrent: store.DefaultMaxConcurrent}
		if u.ExtraEndpoints != nil {
			nu.ExtraEndpoints = *u.ExtraEndpoints
		}
		if u.Remark != nil {
			nu.Remark = *u.Remark
		}
		// 标记缺省时不赋值：CreateUpstream 会把空串归一成默认官方，正是「文件没提
		// 这个字段的新建上游就该拿默认值」的语义；给了才按文件落（含显式空串）。
		if u.Tag != nil {
			t, err := store.NormalizeTag(*u.Tag)
			if err != nil {
				return 0, false, fmt.Errorf("upstream %q: %w", u.Name, err)
			}
			nu.Tag = t
		}
		if u.Enabled != nil {
			nu.Enabled = *u.Enabled
		}
		if u.DailyTokenLimit != nil {
			nu.DailyTokenLimit = *u.DailyTokenLimit
		}
		if u.MonthlyTokenLimit != nil {
			nu.MonthlyTokenLimit = *u.MonthlyTokenLimit
		}
		if u.MaxConcurrent != nil {
			nu.MaxConcurrent = *u.MaxConcurrent
		}
		if u.ExpiresAt.set {
			nu.ExpiresAt = u.ExpiresAt.value()
		}
		id, err := store.CreateUpstream(d, nu)
		if err != nil {
			return 0, false, fmt.Errorf("create upstream %q: %w", u.Name, err)
		}
		// CreateUpstream 的 INSERT 不含 enabled 列（DB 默认启用）；显式
		// enabled=false 需读回再改存（与 createUpstream 同套路）。
		if u.Enabled != nil && !*u.Enabled {
			stored, err := store.GetUpstreamByID(d, id)
			if err != nil {
				return 0, false, fmt.Errorf("reload upstream %q: %w", u.Name, err)
			}
			stored.Enabled = false
			if err := store.UpdateUpstream(d, stored); err != nil {
				return 0, false, fmt.Errorf("disable upstream %q: %w", u.Name, err)
			}
		}
		return id, true, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("get upstream %q: %w", u.Name, err)
	}
	ex.BaseURL = u.BaseURL
	ex.APIKey = u.APIKey
	ex.Format = u.Format
	if u.ExtraEndpoints != nil {
		ex.ExtraEndpoints = *u.ExtraEndpoints
	}
	if u.Remark != nil {
		ex.Remark = *u.Remark
	}
	// 标记 nil = 保留现值（同名覆盖是字段级合并，不是全行替换）；显式空串回官方。
	if u.Tag != nil {
		t, err := store.NormalizeTag(*u.Tag)
		if err != nil {
			return 0, false, fmt.Errorf("upstream %q: %w", u.Name, err)
		}
		ex.Tag = t
	}
	if u.Enabled != nil {
		ex.Enabled = *u.Enabled
	}
	if u.DailyTokenLimit != nil {
		ex.DailyTokenLimit = *u.DailyTokenLimit
	}
	if u.MonthlyTokenLimit != nil {
		ex.MonthlyTokenLimit = *u.MonthlyTokenLimit
	}
	if u.MaxConcurrent != nil {
		ex.MaxConcurrent = *u.MaxConcurrent
	}
	if u.ExpiresAt.set {
		ex.ExpiresAt = u.ExpiresAt.value()
	}
	if err := store.UpdateUpstream(d, ex); err != nil {
		return 0, false, fmt.Errorf("update upstream %q: %w", u.Name, err)
	}
	return ex.ID, false, nil
}
