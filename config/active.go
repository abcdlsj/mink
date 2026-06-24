package config

import (
	"sort"
	"strings"
)

func (c *Config) Resolve(provider, model string) {
	provider = strings.TrimSpace(strings.ToLower(provider))
	model = strings.TrimSpace(model)
	for name, mc := range c.Models {
		if strings.TrimSpace(strings.ToLower(mc.Provider)) != provider {
			continue
		}
		if model != "" && strings.TrimSpace(mc.Model) != model {
			continue
		}
		c.ActiveModel = name
		c.useModel(mc)
		if model != "" {
			c.Model = model
			c.Active.Model = model
		}
		return
	}
	for _, opt := range Detect() {
		if opt.Provider == provider {
			c.useOption(opt, model)
			return
		}
	}
	c.useManual(provider, model)
}

func (c *Config) ResolveNamed(name string) bool {
	return c.ResolveNamedModel(name, "")
}

func (c *Config) ResolveNamedModel(name, model string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	mc, ok := c.Models[name]
	if !ok {
		return false
	}
	if strings.TrimSpace(model) != "" {
		mc.Model = strings.TrimSpace(model)
	}
	c.ActiveModel = name
	c.useModel(mc)
	return true
}

func (c *Config) ResolveActive() bool {
	if c.resolveNamedActive() || c.resolveDirectActive() {
		return true
	}
	if len(c.Models) == 0 {
		return false
	}
	names := make([]string, 0, len(c.Models))
	for name := range c.Models {
		names = append(names, name)
	}
	sort.Strings(names)
	c.ActiveModel = names[0]
	if c.Default == "" {
		c.Default = c.ActiveModel
	}
	c.useModel(c.Models[c.ActiveModel])
	return true
}

func (c *Config) resolveNamedActive() bool {
	for _, name := range []string{c.ActiveModel, c.Default} {
		if name == "" {
			continue
		}
		mc, ok := c.Models[name]
		if !ok {
			continue
		}
		c.ActiveModel = name
		c.useModel(mc)
		return true
	}
	return false
}

func (c *Config) resolveDirectActive() bool {
	if c.Provider == "" || c.Model == "" {
		return false
	}
	c.Active = ModelConfig{
		Provider:      c.Provider,
		Model:         c.Model,
		APIKey:        c.expandKey(c.APIKey),
		BaseURL:       c.BaseURL,
		Headers:       cloneHeaders(c.Headers),
		MaxTokens:     max(c.MaxTokens, 4096),
		ContextWindow: 0,
		Reasoning:     c.Reasoning,
	}
	c.syncActive()
	return true
}

func (c *Config) useOption(opt Option, model string) {
	c.ActiveModel = ""
	c.Provider = opt.Provider
	c.Model = blank(model, opt.Model)
	c.APIKey = opt.APIKey
	c.BaseURL = opt.BaseURL
	c.Active = ModelConfig{
		Provider:      c.Provider,
		Model:         c.Model,
		APIKey:        c.APIKey,
		BaseURL:       c.BaseURL,
		Headers:       cloneHeaders(c.Headers),
		MaxTokens:     c.MaxTokens,
		ContextWindow: 0,
		Reasoning:     c.Reasoning,
	}
}

func (c *Config) useManual(provider, model string) {
	c.ActiveModel = ""
	c.Provider = provider
	if model != "" {
		c.Model = model
	}
	c.Active = ModelConfig{
		Provider:      c.Provider,
		Model:         c.Model,
		APIKey:        c.expandKey(c.APIKey),
		BaseURL:       c.BaseURL,
		Headers:       cloneHeaders(c.Headers),
		MaxTokens:     c.MaxTokens,
		ContextWindow: 0,
		Reasoning:     c.Reasoning,
	}
}
