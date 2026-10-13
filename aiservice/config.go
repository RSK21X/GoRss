// Package aiservice implements an independent OpenAI-compatible AI backend.
package aiservice

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Provider string

const (
	Custom Provider = "custom"
	Magpie Provider = "magpie"
)

type ProviderConfig struct {
	Enabled        bool   `json:"enabled"`
	BaseURL        string `json:"base_url"`
	Model          string `json:"model"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Language       string `json:"language"`
}
type Config struct {
	ActiveProvider Provider       `json:"active_provider"`
	Custom         ProviderConfig `json:"custom"`
	Magpie         ProviderConfig `json:"magpie"`
}

func DefaultConfig() Config {
	return Config{ActiveProvider: Custom, Custom: ProviderConfig{BaseURL: "https://api.openai.com/v1", TimeoutSeconds: 60, Language: "简体中文"}, Magpie: ProviderConfig{BaseURL: "http://127.0.0.1:3425/v1", TimeoutSeconds: 60, Language: "简体中文"}}
}
func (c Config) Provider(p Provider) (ProviderConfig, error) {
	switch p {
	case Custom:
		return c.Custom, nil
	case Magpie:
		return c.Magpie, nil
	}
	return ProviderConfig{}, ErrConfig
}
func (c Config) Validate() error {
	if _, err := c.Provider(c.ActiveProvider); err != nil {
		return err
	}
	for i, p := range []ProviderConfig{c.Custom, c.Magpie} {
		name := []string{"自定义 API", "Magpie"}[i]
		if _, err := validateBase(p.BaseURL); err != nil {
			return fmt.Errorf("%w：%s API 地址须为 HTTPS 或本地 HTTP 根地址", ErrConfig, name)
		}
		if p.TimeoutSeconds < 1 || p.TimeoutSeconds > 600 {
			return fmt.Errorf("%w：%s 超时须为 1–600 秒", ErrConfig, name)
		}
		if len(p.Model) > 512 || strings.ContainsAny(p.Model, "\r\n") {
			return fmt.Errorf("%w：%s 模型 ID 格式无效", ErrConfig, name)
		}
		if len(p.Language) > 128 {
			return fmt.Errorf("%w：%s 目标语言过长", ErrConfig, name)
		}

	}
	return nil
}

var (
	ErrConfig              = errors.New("AI 设置无效")
	ErrDisabled            = errors.New("当前 AI 服务未启用")
	ErrSecretNotFound      = errors.New("尚未保存 API Key")
	ErrSecretStore         = errors.New("无法访问 AI 凭据存储")
	ErrNetwork             = errors.New("AI 连接失败，请检查 API 地址和服务状态")
	ErrProtocol            = errors.New("AI 响应格式不符合协议")
	ErrMissingChoices      = fmt.Errorf("%w：缺少 choices，请确认使用 Chat Completions 接口", ErrProtocol)
	ErrEmptyContent        = fmt.Errorf("%w：模型未返回最终文本（仅 reasoning 也不能视为成功）", ErrProtocol)
	ErrOutputLength        = fmt.Errorf("%w：输出达到 token 上限，未完整生成最终文本", ErrProtocol)
	ErrInvalidJSON         = fmt.Errorf("%w：JSON 无效", ErrProtocol)
	ErrUnsupportedProtocol = fmt.Errorf("%w：不支持的响应协议或 content 类型", ErrProtocol)
	ErrTranslation         = errors.New("AI 翻译响应无效，已完成部分已保留")
)

func validateBase(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, ErrConfig
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	loop := strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loop) {
		return nil, ErrConfig
	}
	if strings.ContainsAny(raw, "\r\n\\") {
		return nil, ErrConfig
	}
	if port := u.Port(); port != "" {
		if _, err := net.LookupPort("tcp", port); err != nil {
			return nil, ErrConfig
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}
func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	f, e := os.Open(path)
	if os.IsNotExist(e) {
		return c, nil
	}
	if e != nil {
		return c, errors.New("AI configuration could not be read")
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil {
		return DefaultConfig(), ErrConfig
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return DefaultConfig(), ErrConfig
	}
	if e = c.Validate(); e != nil {
		return DefaultConfig(), e
	}
	return c, nil
}

// SaveConfig atomically persists only non-secret configuration with mode 0600.
func SaveConfig(path string, c Config) error {
	if e := c.Validate(); e != nil {
		return e
	}
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return ErrConfig
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".ai-config-*")
	if e != nil {
		return errors.New("AI configuration could not be saved")
	}
	name := f.Name()
	defer os.Remove(name)
	ok := false
	defer func() {
		if !ok {
			f.Close()
		}
	}()
	if f.Chmod(0600) != nil {
		return errors.New("AI configuration could not be saved")
	}
	if _, e = f.Write(append(b, '\n')); e != nil {
		return errors.New("AI configuration could not be saved")
	}
	if f.Sync() != nil || f.Close() != nil {
		return errors.New("AI configuration could not be saved")
	}
	ok = true
	if os.Rename(name, path) != nil {
		return errors.New("AI configuration could not be saved")
	}
	return nil
}
