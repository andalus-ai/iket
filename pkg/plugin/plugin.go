package plugin

// Plugin is the base interface all plugins must implement
type Plugin interface {
	Name() string
	Initialize(config map[string]interface{}) error
}
