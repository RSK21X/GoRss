package main

import (
	"context"
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

func (c nativeAIConfig) enabled() bool {
	p, err := aiservice.Config(c).Provider(c.ActiveProvider)
	return err == nil && p.Enabled && strings.TrimSpace(p.Model) != "" && strings.TrimSpace(p.Language) != ""
}

type nativeAISettings struct {
	wasVisible    bool
	config, draft nativeAIConfig
	path          string
	keys          [2]string // ephemeral password input; cleared immediately after capture
	status        [2]string
	busy          [2]bool
	cancel        [2]context.CancelFunc
	generation    [2]uint64
	saving        bool
	loaded        bool
	models        [2][]aiservice.Model
	modelsBase    [2]string
	identityBase  [2]string
}

func (a *nativeReader) loadAISettings() {
	defaults := nativeAIConfig(aiservice.DefaultConfig())
	a.ai.settings.config, a.ai.settings.draft = defaults, defaults
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
			s.config = nativeAIConfig(config)
			if s.draft == defaults {
				s.draft = s.config
			}
		})
	}()
}
func (a *nativeReader) cancelAISettings() {
	s := &a.ai.settings
	for i := range s.cancel {
		if s.cancel[i] != nil {
			s.cancel[i]()
			s.cancel[i] = nil
		}
		s.generation[i]++
		s.busy[i] = false
		s.keys[i] = ""
	}
}
func nativeAIProviderIndex(p aiservice.Provider) int {
	if p == aiservice.Magpie {
		return 1
	}
	return 0
}
func (a *nativeReader) aiSettingsCategory() components.SettingsCategory {
	groups := []components.SettingsGroup{{ID: "ai-active", Title: "文章 AI", Items: []components.SettingsItem{{ID: "ai-provider", Title: "当前服务", Description: "摘要、文章翻译与划词翻译使用这里选中的服务。切换服务保留各自设置。", Build: func(c *ui.Context) {
		sel := components.Select(c, &a.ai.settings.draft.ActiveProvider)
		sel.Trigger.Label("当前 AI 服务").FillWidth().Children(func() {
			label := "自定义 API"
			if a.ai.settings.draft.ActiveProvider == aiservice.Magpie {
				label = "Magpie"
			}
			components.Text(c, label)
		})
		sel.Popup(func(panel ui.Element) {
			for _, p := range []aiservice.Provider{aiservice.Custom, aiservice.Magpie} {
				label := "自定义 API"
				if p == aiservice.Magpie {
					label = "Magpie"
				}
				sel.Item(p).Label(label).FillWidth().Children(func() { components.Text(c, label) })
			}
		})
	}}}}}
	for _, provider := range []aiservice.Provider{a.ai.settings.draft.ActiveProvider} {
		p := provider
		index := nativeAIProviderIndex(p)
		title := "自定义 API"
		if p == aiservice.Magpie {
			title = "Magpie"
		}
		config := &a.ai.settings.draft.Custom
		if p == aiservice.Magpie {
			config = &a.ai.settings.draft.Magpie
		}
		groups = append(groups, components.SettingsGroup{ID: "ai-" + string(p), Title: title, Items: []components.SettingsItem{
			{ID: string(p) + "-enabled", Title: "启用 " + title, Description: "保存后文章操作使用当前服务；工具栏始终保留 AI 入口。", Build: func(c *ui.Context) {
				components.Switch(c, &config.Enabled, "启用 "+title, components.SwitchOptions{HideLabel: true})
			}},
			{ID: string(p) + "-base", Title: "API 地址", Description: "OpenAI 兼容 API 根地址，通常以 /v1 结尾；本地允许 HTTP。", Build: func(c *ui.Context) {
				components.TextBox(c, &config.BaseURL, components.TextBoxOptions{}).Label(title + " API 地址").FillWidth().MinWidth(0)
			}},
			{ID: string(p) + "-model", Title: "模型 ID", Description: "可先获取模型再选择；服务不提供模型列表时可手填。", Build: func(c *ui.Context) {
				ui.Column(c).FillWidth().MinWidth(0).Gap(8).Children(func() {
					components.TextBox(c, &config.Model, components.TextBoxOptions{}).Label(title + " 模型 ID").FillWidth()
					if a.ai.settings.modelsBase[index] == config.BaseURL && len(a.ai.settings.models[index]) > 0 {
						nativeAIModelSelect(c, &config.Model, title, a.ai.settings.models[index])
					}
					components.Button(c, "获取可用模型", components.ButtonSecondary).Disabled(a.ai.settings.busy[index] || a.ai.credentialMutations > 0).OnClick(func() { a.aiSettingsOperation(p, "models") })
				})
			}},
			{ID: string(p) + "-timeout", Title: "超时（秒）", Build: func(c *ui.Context) {
				value := float64(config.TimeoutSeconds)
				input := components.NumberInput(c, &value, 1, 600, 1)
				input.Label(title + " 超时秒数")
				if input.Changed() {
					config.TimeoutSeconds = int(value)
				}
			}},
			{ID: string(p) + "-language", Title: "目标语言", Description: "摘要和翻译的输出语言。", Build: func(c *ui.Context) {
				components.TextBox(c, &config.Language, components.TextBoxOptions{}).Label(title + " 目标语言").FillWidth().MinWidth(0)
			}},
			{ID: string(p) + "-key", Title: "API Key", Description: "凭据仅保存到 macOS Keychain。空白保持现有凭据；本地 Magpie 可不设置。", Build: func(c *ui.Context) {
				ui.Column(c).FillWidth().MinWidth(0).Gap(8).Children(func() {
					components.TextBox(c, &a.ai.settings.keys[index], components.TextBoxOptions{}).Password().Label(title + " API Key").Placeholder("输入新凭据").FillWidth()
					ui.Row(c).Gap(6).Children(func() {
						components.Button(c, "保存到 Keychain", components.ButtonSecondary).Disabled(a.ai.settings.busy[index] || strings.TrimSpace(a.ai.settings.keys[index]) == "").OnClick(func() { a.aiSettingsOperation(p, "key") })
						components.Button(c, "删除凭据", components.ButtonGhost).Disabled(a.ai.settings.busy[index]).OnClick(func() { a.aiSettingsOperation(p, "delete") })
					})
				})
			}},
			{ID: string(p) + "-test", Title: "连接检测", Description: "使用当前表单和新输入的 API Key（空白使用已保存凭据）。检测不调用模型；测试模型会发送请求。", Build: func(c *ui.Context) {
				ui.Column(c).FillWidth().MinWidth(0).Gap(8).Children(func() {
					ui.Row(c).Gap(8).Children(func() {
						components.Button(c, "检测协议", components.ButtonSecondary).Disabled(a.ai.settings.busy[index]).OnClick(func() { a.aiSettingsOperation(p, "probe") })
						components.Button(c, "测试模型", components.ButtonSecondary).Disabled(a.ai.settings.busy[index]).OnClick(func() { a.aiSettingsOperation(p, "test") })
					})
					if a.ai.settings.busy[index] {
						components.Spinner(c, components.SpinnerSpokes).Size(16, 16)
					}
					if p == aiservice.Magpie {
						identity := "Magpie 身份尚未确认"
						if a.ai.settings.identityBase[index] == config.BaseURL && config.BaseURL != "" {
							identity = "Magpie 身份已确认"
						}
						components.Text(c, identity).Wrap().FontSize(12)
					}
					components.Text(c, a.ai.settings.status[index]).Wrap().FontSize(12)
				})
			}},
		}})
	}
	groups = append(groups, components.SettingsGroup{ID: "ai-save", Title: "", Items: []components.SettingsItem{{ID: "ai-save-config", Title: "保存 AI 设置", Description: "", Build: func(c *ui.Context) {
		components.Button(c, "保存 AI 设置", components.ButtonPrimary).Disabled(!a.ai.settings.loaded || a.ai.settings.saving || a.ai.settings.draft == a.ai.settings.config).OnClick(a.saveAISettings)
	}}}})
	// Keep every item searchable and let Settings render its standard row/group.
	for gi := range groups {
		for ii := range groups[gi].Items {
			build := groups[gi].Items[ii].Build
			groups[gi].Items[ii].Build = func(c *ui.Context) {
				controls := ui.Column(c).MinWidth(0).Gap(8)
				if nativeSettingsNarrow(c) {
					controls.FillWidth().MaxWidth(560)
				} else {
					controls.Width(320).Shrink(0)
				}
				controls.Children(func() { build(c) })
			}
		}
	}
	return components.SettingsCategory{ID: "ai", Title: "AI", Icon: lucide.Sparkles(), Description: "摘要、翻译与凭据", Groups: groups}
}
func (a *nativeReader) aiSettingsOperation(provider aiservice.Provider, kind string) {
	s := &a.ai.settings
	i := nativeAIProviderIndex(provider)
	if s.busy[i] || a.ai.credentialMutations > 0 {
		return
	}
	if s.cancel[i] != nil {
		s.cancel[i]()
	}
	s.generation[i]++
	token := s.generation[i]
	ctx, cancel := context.WithCancel(a.ctx)
	s.cancel[i] = cancel
	s.busy[i] = true
	s.status[i] = "正在使用当前表单检测…"
	config := aiservice.Config(s.draft)
	providerConfig, _ := config.Provider(provider)
	mutating := kind == "key" || kind == "delete"
	if mutating {
		a.beginAICredentialMutation(provider)
	}
	if kind == "probe" {
		s.identityBase[i] = ""
	}
	if kind == "models" || kind == "probe" {
		s.models[i], s.modelsBase[i] = nil, ""
	}
	key := s.keys[i]
	if kind == "key" {
		s.keys[i] = ""
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
			if a.ctx.Err() != nil || ctx.Err() != nil || s.generation[i] != token {
				return
			}
			cancel()
			s.cancel[i] = nil
			s.busy[i] = false
			if kind == "probe" || kind == "test" || kind == "models" {
				current, _ := aiservice.Config(s.draft).Provider(provider)
				if current != providerConfig || (kind != "key" && s.keys[i] != key) {
					s.status[i] = "设置已变化，请重新检测"
					return
				}
			}
			if err != nil {
				s.status[i] = nativeAISettingsError(err)
			} else {
				s.status[i] = message
				if kind == "models" || kind == "probe" {
					s.models[i], s.modelsBase[i] = models, providerConfig.BaseURL
				}
				if kind == "probe" && provider == aiservice.Magpie {
					s.identityBase[i] = providerConfig.BaseURL
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
	draft := s.draft
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
			s.config = draft
			a.resetAI()
			clear(a.ai.cache)
			a.notice = "AI 设置已保存"
		})
	}()
}
func nativeAIExecute(ctx context.Context, config nativeAIConfig, kind, source, title, selected string, regenerate bool) (string, error) {
	service, err := aiservice.New(aiservice.Config(config), aiservice.NewKeychainStore())
	if err != nil {
		return "", err
	}
	if regenerate {
		service.ForceRefresh()
	}
	switch kind {
	case "summary":
		return service.Summarize(ctx, source)
	case "translation":
		value, err := service.TranslateMarkdown(ctx, source, aiservice.TranslationOptions{})
		return value.Markdown, err
	case "selection":
		return service.TranslateSelection(ctx, selected)
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
	sel.Popup(func(panel ui.Element) {
		panel.Width(320).Children(func() {
			ui.Scroll(c).FillWidth().MaxHeight(280).Children(func() {
				for _, model := range models {
					label := model.ID
					if model.DisplayName != "" && model.DisplayName != model.ID {
						label = model.DisplayName + " · " + model.ID
					}
					sel.Item(model.ID).Key(model.ID).FillWidth().Children(func() { components.Text(c, label).SingleLine() })
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
	i := nativeAIProviderIndex(provider)
	a.ai.settings.models[i], a.ai.settings.modelsBase[i], a.ai.settings.identityBase[i] = nil, "", ""
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
