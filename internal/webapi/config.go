package webapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/model"
)

// 配置导出/导入：上游（含真实 API Key、模型列表、额度）与模型别名。别名
// 绑定按上游名引用（而非实例内的自增 ID），保证导出文件跨实例可用。
//
// 导入语义：同名覆盖——上游全字段覆盖、模型列表精确替换；别名绑定整体替换。
// 文件里没出现的现有配置原样保留。字段级例外：enabled / 两个 token 上限 /
// models 在文件里缺省时保留现状（导入侧用指针/nil 区分「没给」），其余字段
// 一律以文件为准。绑定指向的上游在文件与库中都不存在时丢弃该绑定（计数
// bindings_dropped）；一个可解析绑定都不剩的别名整个跳过。文件内重名条目
// （上游/别名/单上游内模型）整体 400 拒绝。导入非单事务：中途 DB 失败时
// 已提交的条目会保留，直接重导同一文件即可收敛（同名覆盖幂等）。

const configVersion = 1

type configUpstreamModel struct {
	ModelName       string `json:"model_name"`
	Manual          bool   `json:"manual"`
	ContextLength   int    `json:"context_length"`
	MaxOutputLength int    `json:"max_output_length"`
}

type configUpstream struct {
	Name              string                `json:"name"`
	BaseURL           string                `json:"base_url"`
	APIKey            string                `json:"api_key"`
	Format            string                `json:"format"`
	Enabled           *bool                 `json:"enabled"`
	DailyTokenLimit   *int                  `json:"daily_token_limit"`
	MonthlyTokenLimit *int                  `json:"monthly_token_limit"`
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
	ups, err := model.ListUpstreams(a.db)
	if err != nil {
		logger.Error("admin: export config list upstreams failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	aliases, err := model.ListAliases(a.db)
	if err != nil {
		logger.Error("admin: export config list aliases failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	out := configFile{Version: configVersion, ExportedAt: time.Now(),
		Upstreams: make([]configUpstream, 0, len(ups)), Aliases: make([]configAlias, 0, len(aliases))}
	for _, u := range ups {
		cu := configUpstream{Name: u.Name, BaseURL: u.BaseURL, APIKey: u.APIKey, Format: u.Format,
			DailyTokenLimit: &u.DailyTokenLimit, MonthlyTokenLimit: &u.MonthlyTokenLimit}
		en := u.Enabled
		cu.Enabled = &en
		models, err := model.ListModels(a.db, u.ID)
		if err != nil {
			logger.Error("admin: export config list models failed", "upstream", u.Name, "err", err)
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		cu.Models = make([]configUpstreamModel, 0, len(models))
		for _, m := range models {
			cu.Models = append(cu.Models, configUpstreamModel{ModelName: m.ModelName, Manual: m.Manual,
				ContextLength: m.ContextLength, MaxOutputLength: m.MaxOutputLength})
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
				models := make([]model.UpstreamModel, 0, len(u.Models))
				for _, m := range u.Models {
					models = append(models, model.UpstreamModel{ModelName: m.ModelName, Manual: m.Manual,
						ContextLength: m.ContextLength, MaxOutputLength: m.MaxOutputLength})
				}
				if err := model.ReplaceModelsExact(d, id, models); err != nil {
					return fmt.Errorf("upstream %q models: %w", u.Name, err)
				}
			}
		}
		for i := range in.Aliases {
			al := &in.Aliases[i]
			bindings := make([]model.AliasBinding, 0, len(al.Bindings))
			for _, b := range al.Bindings {
				id, ok := ids[b.Upstream]
				if !ok {
					ex, err := model.GetUpstreamByName(d, b.Upstream)
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
				bindings = append(bindings, model.AliasBinding{UpstreamID: id, ModelName: b.ModelName})
			}
			if len(bindings) == 0 {
				res.AliasesSkipped++
				continue
			}
			ex, err := model.GetAliasByName(d, al.Name)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("alias %q: %w", al.Name, err)
			}
			if err == nil {
				ex.Bindings = bindings
				if err := model.UpdateAlias(d, ex); err != nil {
					return fmt.Errorf("alias %q: %w", al.Name, err)
				}
				res.AliasesUpdated++
			} else if _, err := model.CreateAlias(d, &model.ModelAlias{Name: al.Name, Bindings: bindings}); err != nil {
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
		if u.DailyTokenLimit != nil && *u.DailyTokenLimit < 0 {
			return fmt.Errorf("upstreams[%d]: daily_token_limit must be >= 0", i)
		}
		if u.MonthlyTokenLimit != nil && *u.MonthlyTokenLimit < 0 {
			return fmt.Errorf("upstreams[%d]: monthly_token_limit must be >= 0", i)
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
	ex, err := model.GetUpstreamByName(d, u.Name)
	if errors.Is(err, sql.ErrNoRows) {
		nu := &model.Upstream{Name: u.Name, BaseURL: u.BaseURL, APIKey: u.APIKey, Format: u.Format, Enabled: true}
		if u.Enabled != nil {
			nu.Enabled = *u.Enabled
		}
		if u.DailyTokenLimit != nil {
			nu.DailyTokenLimit = *u.DailyTokenLimit
		}
		if u.MonthlyTokenLimit != nil {
			nu.MonthlyTokenLimit = *u.MonthlyTokenLimit
		}
		id, err := model.CreateUpstream(d, nu)
		if err != nil {
			return 0, false, fmt.Errorf("create upstream %q: %w", u.Name, err)
		}
		// CreateUpstream 的 INSERT 不含 enabled 列（DB 默认启用）；显式
		// enabled=false 需读回再改存（与 createUpstream 同套路）。
		if u.Enabled != nil && !*u.Enabled {
			stored, err := model.GetUpstreamByID(d, id)
			if err != nil {
				return 0, false, fmt.Errorf("reload upstream %q: %w", u.Name, err)
			}
			stored.Enabled = false
			if err := model.UpdateUpstream(d, stored); err != nil {
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
	if u.Enabled != nil {
		ex.Enabled = *u.Enabled
	}
	if u.DailyTokenLimit != nil {
		ex.DailyTokenLimit = *u.DailyTokenLimit
	}
	if u.MonthlyTokenLimit != nil {
		ex.MonthlyTokenLimit = *u.MonthlyTokenLimit
	}
	if err := model.UpdateUpstream(d, ex); err != nil {
		return 0, false, fmt.Errorf("update upstream %q: %w", u.Name, err)
	}
	return ex.ID, false, nil
}
