package config

type RuntimeState struct {
	AppName      string         `json:"app_name"`
	ConfigPath   string         `json:"config_path"`
	LogPath      string         `json:"log_path"`
	LogDirectory string         `json:"log_directory"`
	Config       Config         `json:"config"`
	Providers    ProviderStatus `json:"providers"`
	Backend      BackendStatus  `json:"backend"`
	PlatformNote string         `json:"platform_note"`
}

type ProviderStatus struct {
	OpenAIConfigured   bool `json:"openai_configured"`
	DeepgramConfigured bool `json:"deepgram_configured"`
	CartesiaConfigured bool `json:"cartesia_configured"`
}

type BackendStatus struct {
	Enabled   bool   `json:"enabled"`
	Running   bool   `json:"running"`
	Mode      string `json:"mode"`
	Transport string `json:"transport"`
	Endpoint  string `json:"endpoint"`
	Service   string `json:"service"`
	Version   string `json:"version"`
	Status    string `json:"status"`
	LastError string `json:"last_error"`
}
