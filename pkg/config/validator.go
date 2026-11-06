package config

import "fmt"

// ValidationRule defines a configuration validation rule.
type ValidationRule interface {
	Validate(cfg *Config) error
}

// ConfigValidator validates configuration using the configured rule set.
type ConfigValidator struct {
	rules []ValidationRule
}

// NewConfigValidator creates a validator with the repository's split rules.
func NewConfigValidator() *ConfigValidator {
	return &ConfigValidator{
		rules: []ValidationRule{
			&ServerConfigRule{},
			&SecurityConfigRule{},
			&ServicesConfigRule{},
			&PluginsConfigRule{},
		},
	}
}

// AddRule adds a custom validation rule.
func (v *ConfigValidator) AddRule(rule ValidationRule) {
	v.rules = append(v.rules, rule)
}

// Validate validates the configuration using all registered rules.
func (v *ConfigValidator) Validate(cfg *Config) error {
	for _, rule := range v.rules {
		if err := rule.Validate(cfg); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}
	}
	return nil
}
