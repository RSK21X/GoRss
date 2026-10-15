package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"gorss/aiservice"
)

type nativeAIConfig aiservice.Config

func (c nativeAIConfig) clone() nativeAIConfig { return nativeAIConfig(aiservice.Config(c).Clone()) }
func (c nativeAIConfig) equal(other nativeAIConfig) bool {
	return aiservice.Config(c).Equal(aiservice.Config(other))
}
func (c nativeAIConfig) enabled() bool {
	p, err := aiservice.Config(c).Provider(c.ActiveProvider)
	return err == nil && p.Enabled && strings.TrimSpace(p.Model) != "" && strings.TrimSpace(p.Language) != ""
}

type nativeAIProviderState struct {
	key, status              string
	busy                     bool
	cancel                   context.CancelFunc
	generation               uint64
	models                   []aiservice.Model
	modelsBase, identityBase string
}
type nativeAISettings struct {
	wasVisible     bool
	config, draft  nativeAIConfig
	path           string
	providers      map[aiservice.Provider]*nativeAIProviderState
	saving, loaded bool
	tab            string
	removeOpen     bool
}

func (s *nativeAISettings) provider(p aiservice.Provider) *nativeAIProviderState {
	if s.providers == nil {
		s.providers = map[aiservice.Provider]*nativeAIProviderState{}
	}
	if s.providers[p] == nil {
		s.providers[p] = &nativeAIProviderState{}
	}
	return s.providers[p]
}
func (a *nativeReader) loadAISettings() {
	defaults := nativeAIConfig(aiservice.DefaultConfig())
	a.ai.settings.config, a.ai.settings.draft = defaults.clone(), defaults.clone()
	directory, err := os.UserConfigDir()
	if err != nil {
		a.errorText = "无法定位 AI 设置目录"
		return
	}
	a.ai.settings.path = filepath.Join(directory, "GoRss", "ai.json")
	path := a.ai.settings.path
	go func() {
		config, err := aiservice.LoadConfig(path)
		a.window.Update(func() {
			if a.ctx.Err() != nil {
				return
			}
			s := &a.ai.settings
			s.loaded = true
			if err != nil {
				a.errorText = err.Error()
				return
			}
			s.config = nativeAIConfig(config).clone()
			if s.draft.equal(defaults) {
				s.draft = s.config.clone()
			}
		})
	}()
}
func (a *nativeReader) cancelAISettings() {
	a.ai.settings.removeOpen = false
	for _, state := range a.ai.settings.providers {
		if state.cancel != nil {
			state.cancel()
			state.cancel = nil
		}
		state.generation++
		state.busy = false
		state.key = ""
	}
}
func (a *nativeReader) addAIProvider() {
	s := &a.ai.settings
	if len(s.draft.Providers) >= 64 {
		return
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		a.errorText = "无法创建供应商标识"
		return
	}
	p := aiservice.Provider("provider-" + hex.EncodeToString(id[:]))
	s.draft = s.draft.clone()
	s.draft.Providers = append(s.draft.Providers, aiservice.NamedProvider{ID: p, Config: aiservice.ProviderConfig{Name: "新供应商", TimeoutSeconds: 60, Language: "简体中文", Stream: true}})
	s.draft.ActiveProvider = p
	s.tab = "connection"
}
func (a *nativeReader) removeAIProvider(p aiservice.Provider) {
	if p == aiservice.Custom || p == aiservice.Magpie {
		return
	}
	s := &a.ai.settings
	state := s.provider(p)
	if state.cancel != nil {
		state.cancel()
	}
	state.generation++
	state.busy = false
	state.key = ""
	delete(s.providers, p)
	s.draft = s.draft.clone()
	for i, entry := range s.draft.Providers {
		if entry.ID == p {
			s.draft.Providers = append(s.draft.Providers[:i], s.draft.Providers[i+1:]...)
			break
		}
	}
	if s.draft.ActiveProvider == p {
		s.draft.ActiveProvider = aiservice.Custom
	}
	s.removeOpen = false
}
func (a *nativeReader) aiSettingsCategory() components.SettingsCategory {
	s := &a.ai.settings
	config := aiservice.Config(s.draft)
	p := config.ActiveProvider
	pc := (*aiservice.Config)(&s.draft).ProviderDraft(p)
	if pc == nil {
		s.draft.ActiveProvider = aiservice.Custom
		p = aiservice.Custom
		pc = &s.draft.Custom
	}
	state := s.provider(p)
	title := config.ProviderName(p)
	groups := []components.SettingsGroup{{ID: "ai-active", Title: "供应商", Items: []components.SettingsItem{
		{ID: "ai-provider", Title: "当前服务", Description: "每个供应商单独保存模型、密钥和提示词。保存后用于摘要和翻译。", Build: func(c *ui.Context) {
			ui.Column(c).FillWidth().MinWidth(0).Gap(10).Children(func() {
				sel := components.Select(c, &s.draft.ActiveProvider)
				sel.Trigger.Label("当前 AI 服务").FillWidth().Children(func() {
					components.Text(c, aiservice.Config(s.draft).ProviderName(s.draft.ActiveProvider)).SingleLine()
				})
				sel.Popup(func(panel ui.Element) {
					for _, id := range aiservice.Config(s.draft).ProviderIDs() {
						c.Key(id)
						sel.Item(id).Label(aiservice.Config(s.draft).ProviderName(id)).FillWidth().Children(func() { components.Text(c, aiservice.Config(s.draft).ProviderName(id)).SingleLine() })
					}
				})
				ui.Row(c).Gap(8).Children(func() {
					components.Button(c, "添加供应商", components.ButtonSecondary).Disabled(!s.loaded || s.saving || len(s.draft.Providers) >= 64).OnClick(a.addAIProvider)
					if p != aiservice.Custom && p != aiservice.Magpie {
						components.Button(c, "移除配置", components.ButtonGhost).Disabled(s.saving || a.ai.credentialMutations > 0).OnClick(func() { s.removeOpen = true })
					}
				})
				ui.Row(c).Gap(4).Children(func() {
					for _, tab := range []struct{ id, label string }{{"connection", "连接与模型"}, {"prompts", "提示词"}} {
						tab := tab
						style := components.ButtonGhost
						if s.tab == tab.id || (s.tab == "" && tab.id == "connection") {
							style = components.ButtonSecondary
						}
						components.Button(c, tab.label, style).OnClick(func() { s.tab = tab.id })
					}
				})
			})
		}},
	}}}
	if s.tab == "prompts" || strings.TrimSpace(a.settingsState.Query) != "" {
		groups = append(groups, components.SettingsGroup{ID: "ai-prompts-" + string(p), Title: "自定义提示词", Items: []components.SettingsItem{
			{ID: "summary-prompt", Title: "摘要提示词", Description: "控制摘要的语言、长度和结构。{language} 会替换为目标语言，{kind} 表示内容类型。留空使用默认提示词。", Build: func(c *ui.Context) {
				nativeAIPromptEditor(c, &pc.SummaryPrompt, "摘要", aiservice.DefaultSummaryPrompt)
			}},
			{ID: "translation-prompt", Title: "翻译提示词", Description: "控制译文风格、术语和语气。系统会附加段落标识与格式保持要求；不会修改订阅原文。", Build: func(c *ui.Context) {
				nativeAIPromptEditor(c, &pc.TranslationPrompt, "翻译", aiservice.DefaultTranslationPrompt)
			}},
		}})
	}
	if s.tab != "prompts" || strings.TrimSpace(a.settingsState.Query) != "" {
		groups = append(groups, components.SettingsGroup{ID: "ai-connection-" + string(p), Title: "连接", Items: []components.SettingsItem{
			{ID: "provider-name", Title: "显示名称", Description: "用于识别供应商，不影响连接与密钥。", Build: func(c *ui.Context) {
				components.TextBox(c, &pc.Name, components.TextBoxOptions{}).Label("供应商名称").Placeholder(title).FillWidth()
			}},
			{ID: "provider-enabled", Title: "启用状态", Description: "启用并保存后，可以主动生成摘要和译文。", Build: func(c *ui.Context) {
				components.Switch(c, &pc.Enabled, "启用 "+title, components.SwitchOptions{HideLabel: true})
			}},
			{ID: "provider-base", Title: "API 地址", Description: "OpenAI 兼容接口根地址，通常以 /v1 结尾。", Build: func(c *ui.Context) {
				components.TextBox(c, &pc.BaseURL, components.TextBoxOptions{}).Label(title + " API 地址").Placeholder("https://…/v1").FillWidth()
			}},
			{ID: "provider-key", Title: "API Key", Description: "空输入保留已有凭据。保存到 Keychain 后立即生效；本地 Magpie 可以留空。", Build: func(c *ui.Context) {
				components.TextBox(c, &state.key, components.TextBoxOptions{}).Password().Label(title + " API Key").Placeholder("输入新凭据").FillWidth()
				ui.Row(c).Gap(8).Children(func() {
					components.Button(c, "保存到 Keychain", components.ButtonSecondary).Disabled(state.busy || a.ai.credentialMutations > 0 || strings.TrimSpace(state.key) == "").OnClick(func() { a.aiSettingsOperation(p, "key") })
					components.Button(c, "删除凭据", components.ButtonGhost).Disabled(state.busy || a.ai.credentialMutations > 0).OnClick(func() { a.aiSettingsOperation(p, "delete") })
				})
			}},
			{ID: "provider-model", Title: "模型 ID", Description: "获取模型列表后选择，也可以直接填写。", Build: func(c *ui.Context) {
				components.TextBox(c, &pc.Model, components.TextBoxOptions{}).Label(title + " 模型 ID").FillWidth()
				if state.modelsBase == pc.BaseURL && len(state.models) > 0 {
					nativeAIModelSelect(c, &pc.Model, title, state.models)
				}
				ui.Row(c).Gap(8).Children(func() {
					components.Button(c, "检测协议", components.ButtonSecondary).Disabled(state.busy || a.ai.credentialMutations > 0).OnClick(func() { a.aiSettingsOperation(p, "probe") })
					components.Button(c, "获取可用模型", components.ButtonGhost).Disabled(state.busy || a.ai.credentialMutations > 0).OnClick(func() { a.aiSettingsOperation(p, "models") })
				})
				components.Button(c, "测试模型", components.ButtonGhost).Disabled(state.busy || a.ai.credentialMutations > 0).OnClick(func() { a.aiSettingsOperation(p, "test") })
				if state.busy {
					components.Spinner(c, components.SpinnerSpokes).Size(16, 16)
				}
				if state.status != "" {
					components.Text(c, state.status).Wrap().FontSize(12).TextColor(c.Theme().TextMuted)
				}
				if p == aiservice.Magpie && state.identityBase == pc.BaseURL {
					components.Text(c, "已验证 Magpie 服务身份").FontSize(12).TextColor(c.Theme().TextMuted)
				}
			}},
		}}, components.SettingsGroup{ID: "ai-output-" + string(p), Title: "生成与显示", Items: []components.SettingsItem{
			{ID: "provider-stream", Title: "逐步输出", Description: "收到文本后逐步显示。兼容服务不支持流式时可关闭。", Build: func(c *ui.Context) {
				components.Switch(c, &pc.Stream, "流式显示", components.SwitchOptions{HideLabel: true})
			}},
			{ID: "provider-language", Title: "目标语言", Description: "摘要和翻译的默认输出语言。", Build: func(c *ui.Context) {
				components.TextBox(c, &pc.Language, components.TextBoxOptions{}).Label(title + " 目标语言").FillWidth()
			}},
			{ID: "provider-timeout", Title: "超时（秒）", Description: "包括流式内容接收，范围为 1–600 秒。", Build: func(c *ui.Context) {
				value := float64(pc.TimeoutSeconds)
				input := components.NumberInput(c, &value, 1, 600, 1).Label(title + " 超时秒数")
				if input.Changed() {
					pc.TimeoutSeconds = int(value)
				}
			}},
		}})
	}
	for gi := range groups {
		for ii := range groups[gi].Items {
			build := groups[gi].Items[ii].Build
			wide := strings.HasSuffix(groups[gi].Items[ii].ID, "-prompt")
			groups[gi].Items[ii].Build = func(c *ui.Context) {
				controls := ui.Column(c).MinWidth(0).Gap(10)
				if nativeSettingsNarrow(c) {
					controls.FillWidth().MaxWidth(620)
				} else if wide {
					controls.Width(560).Shrink(0)
				} else {
					controls.Width(320).Shrink(0)
				}
				controls.Children(func() { build(c) })
			}
		}
	}
	return components.SettingsCategory{ID: "ai", Title: "AI", Icon: lucide.Sparkles(), Description: "供应商、模型与提示词", Groups: groups}
}
func nativeAIPromptEditor(c *ui.Context, value *string, label, fallback string) {
	ui.TextArea(c, value).Label(label+"提示词编辑器").Placeholder(fallback).FillWidth().Height(190).Padding(12).FontSize(13).LineHeight(1.5).Radius(8).Background(c.Theme().Surface).Border(1, c.Theme().Border)
	ui.Row(c).FillWidth().Gap(8).Children(func() {
		components.Text(c, fmt.Sprintf("%d / 16000 字节", len(*value))).FontSize(11).TextColor(c.Theme().TextMuted).Grow(1)
		components.Button(c, "填入默认"+label, components.ButtonGhost).OnClick(func() { *value = fallback })
		components.Button(c, "清空自定义"+label, components.ButtonGhost).Disabled(*value == "").OnClick(func() { *value = "" })
	})
}

func (a *nativeReader) aiSettingsFooter(c *ui.Context) {
	s := &a.ai.settings
	ui.Row(c).FillWidth().Shrink(0).Padding(12, 24).Gap(10).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Children(func() {
		label := "已保存"
		if !s.loaded {
			label = "正在读取设置…"
		} else if !s.draft.equal(s.config) {
			label = "有未保存的更改"
		}
		components.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted).Grow(1)
		components.Button(c, "放弃更改", components.ButtonGhost).Disabled(s.saving || s.draft.equal(s.config)).OnClick(func() { a.cancelAISettings(); s.draft = s.config.clone() })
		components.Button(c, "保存 AI 设置", components.ButtonPrimary).Disabled(!s.loaded || s.saving || s.draft.equal(s.config)).OnClick(a.saveAISettings)
	})
	components.Dialog(c, &s.removeOpen, components.DialogOptions{Label: "移除供应商配置"}, func(panel ui.Element) {
		panel.Width(440).MaxWidth(nativeDialogWidth(c)).Gap(12)
		components.Text(c, "移除 "+aiservice.Config(s.draft).ProviderName(s.draft.ActiveProvider)+"？").Bold()
		components.Text(c, "保存后移除该服务配置。Keychain 凭据独立管理，如需清理请先使用“删除凭据”。").Wrap()
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			components.Button(c, "取消", components.ButtonGhost).OnClick(func() { s.removeOpen = false })
			components.Button(c, "移除配置", components.ButtonPrimary).OnClick(func() { a.removeAIProvider(s.draft.ActiveProvider) })
		})
	})
}

func (a *nativeReader) aiSettingsOperation(provider aiservice.Provider, kind string) {
	s := &a.ai.settings
	state := s.provider(provider)
	if state.busy || a.ai.credentialMutations > 0 {
		return
	}
	if state.cancel != nil {
		state.cancel()
	}
	state.generation++
	token := state.generation
	ctx, cancel := context.WithCancel(a.ctx)
	state.cancel = cancel
	state.busy = true
	state.status = "正在使用当前表单检测…"
	config := aiservice.Config(s.draft).Clone()
	providerConfig, _ := config.Provider(provider)
	mutating := kind == "key" || kind == "delete"
	if mutating {
		a.beginAICredentialMutation(provider)
	}
	if kind == "probe" {
		state.identityBase = ""
	}
	if kind == "models" || kind == "probe" {
		state.models, state.modelsBase = nil, ""
	}
	key := state.key
	if kind == "key" {
		state.key = ""
	}
	// Editing configuration while a probe runs cannot attach its result to new input.
	go func() {
		store := aiservice.NewKeychainStore()
		var err error
		message := ""
		var models []aiservice.Model
		switch kind {
		case "key":
			if strings.ContainsAny(key, "\r\n") || len(key) > 16384 {
				err = aiservice.ErrConfig
			} else {
				var service *aiservice.Service
				service, err = aiservice.New(aiservice.DefaultConfig(), store)
				if err == nil {
					err = service.SetKey(provider, key)
				}
			}
			key = ""
			message = "凭据已保存到 Keychain"
		case "delete":
			var service *aiservice.Service
			service, err = aiservice.New(aiservice.DefaultConfig(), store)
			if err == nil {
				err = service.DeleteKey(provider)
			}
			message = "凭据已删除"
		default:
			var service *aiservice.Service
			service, err = aiservice.NewForProvider(provider, providerConfig, key, store)
			if err == nil {
				if kind == "probe" {
					if provider == aiservice.Magpie {
						err = service.DetectMagpie(ctx)
					}
					if err == nil {
						models, err = service.ListModels(ctx, provider)
					}
					message = "协议检测通过（/v1/models）"
					if provider == aiservice.Magpie {
						message = "连接检测通过，已获取可用模型"
					}
				} else if kind == "models" {
					models, err = service.ListModels(ctx, provider)
					message = fmt.Sprintf("已获取 %d 个可用模型", len(models))
				} else {
					err = service.TestModel(ctx, provider)
					message = "模型测试通过（真实请求）"
				}
			}
		}
		a.window.Update(func() {
			if mutating {
				a.ai.credentialMutations--
			}
			if a.ctx.Err() != nil || ctx.Err() != nil || state.generation != token {
				return
			}
			cancel()
			state.cancel = nil
			state.busy = false
			if kind == "probe" || kind == "test" || kind == "models" {
				current, _ := aiservice.Config(s.draft).Provider(provider)
				if current != providerConfig || (kind != "key" && state.key != key) {
					state.status = "设置已变化，请重新检测"
					return
				}
			}
			if err != nil {
				state.status = nativeAISettingsError(err)
			} else {
				state.status = message
				if kind == "models" || kind == "probe" {
					state.models, state.modelsBase = models, providerConfig.BaseURL
				}
				if kind == "probe" && provider == aiservice.Magpie {
					state.identityBase = providerConfig.BaseURL
				}
			}
		})
	}()
}
func (a *nativeReader) saveAISettings() {
	s := &a.ai.settings
	if s.saving {
		return
	}
	draft := s.draft.clone()
	if err := aiservice.Config(draft).Validate(); err != nil {
		a.errorText = err.Error()
		return
	}
	s.saving = true
	path := s.path
	go func() {
		err := os.MkdirAll(filepath.Dir(path), 0700)
		if err == nil {
			err = aiservice.SaveConfig(path, aiservice.Config(draft))
		}
		a.window.Update(func() {
			if a.ctx.Err() != nil {
				return
			}
			s.saving = false
			if err != nil {
				a.errorText = err.Error()
				return
			}
			s.config = draft.clone()
			a.resetAI()
			clear(a.ai.cache)
			a.notice = "AI 设置已保存"
		})
	}()
}
func (a *nativeReader) newAIService(config nativeAIConfig) (*aiservice.Service, error) {
	if a.ai.serviceFactory != nil {
		return a.ai.serviceFactory(aiservice.Config(config).Clone())
	}
	return aiservice.New(aiservice.Config(config), aiservice.NewKeychainStore())
}
func (a *nativeReader) executeAI(ctx context.Context, config nativeAIConfig, kind, source, selected string, regenerate bool, onDelta func(string)) (string, error) {
	service, err := a.newAIService(config)
	if err != nil {
		return "", err
	}
	if regenerate {
		service.ForceRefresh()
	}
	switch kind {
	case "summary":
		return service.SummarizeStream(ctx, source, onDelta)
	case "selection":
		return service.TranslateSelectionStream(ctx, selected, onDelta)
	}
	return "", fmt.Errorf("未知 AI 操作")
}

func nativeAIModelSelect(c *ui.Context, selected *string, provider string, models []aiservice.Model) {
	sel := components.Select(c, selected)
	sel.Trigger.Label(provider + " 可用模型").FillWidth().Children(func() {
		label := *selected
		if label == "" {
			label = "选择可用模型"
		}
		components.Text(c, label).SingleLine()
	})
	query := ui.Local(sel.Trigger, "modelSearch", func() string { return "" })
	if !sel.Open() {
		*query = ""
	}
	sel.Popup(func(panel ui.Element) {
		panel.Width(320).Children(func() {
			sel.SearchField(query).Label(provider + " 搜索模型").Placeholder("搜索模型名称或 ID")
			ui.Scroll(c).FillWidth().MaxHeight(280).Children(func() {
				count := 0
				for _, model := range models {
					if q := strings.ToLower(strings.TrimSpace(*query)); q != "" && !strings.Contains(strings.ToLower(model.ID+" "+model.DisplayName), q) {
						continue
					}
					count++
					label := model.ID
					if model.DisplayName != "" && model.DisplayName != model.ID {
						label = model.DisplayName + " · " + model.ID
					}
					c.Key(model.ID)
					sel.Item(model.ID).FillWidth().Children(func() { components.Text(c, label).SingleLine() })
				}
				if count == 0 {
					components.Text(c, "没有匹配的模型").TextColor(c.Theme().TextMuted)
				}
			})
		})
	})
}

// Invalidate generations before touching Keychain so queued old completions
// cannot restore results, even if the credential operation fails or is dismissed.
func (a *nativeReader) beginAICredentialMutation(provider aiservice.Provider) {
	a.ai.credentialMutations++
	a.resetAI()
	clear(a.ai.cache)
	state := a.ai.settings.provider(provider)
	state.models, state.modelsBase, state.identityBase = nil, "", ""
}

func nativeAISettingsError(err error) string {
	var httpErr *aiservice.HTTPError
	if errors.As(err, &httpErr) && (httpErr.StatusCode == 404 || httpErr.StatusCode == 405) {
		return "服务未提供该检测接口；请核对 API 地址，模型 ID 可手动填写。"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "检测超时，请检查 API 地址与本地服务是否已启动。"
	}
	return err.Error()
}
