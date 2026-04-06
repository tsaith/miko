package config

type RuntimeState struct {
	AppName      string         `json:"app_name"`
	ConfigPath   string         `json:"config_path"`
	LogPath      string         `json:"log_path"`
	LogDirectory string         `json:"log_directory"`
	Config       Config         `json:"config"`
	Providers    ProviderStatus `json:"providers"`
	PlatformNote string         `json:"platform_note"`
}

type ProviderStatus struct {
	OpenAIConfigured   bool `json:"openai_configured"`
	DeepgramConfigured bool `json:"deepgram_configured"`
	CartesiaConfigured bool `json:"cartesia_configured"`
}
