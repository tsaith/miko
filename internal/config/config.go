package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

const (
	DefaultCartesiaVoiceID = "6eb8965c-e295-47bd-a9e4-3eeebb3abcff"
	DefaultCartesiaModelID = "sonic-3"
	DefaultCartesiaVersion = "2025-04-16"
	DefaultOpenAIModel     = "gpt-4o-mini"
	DefaultDeepgramModel   = "nova-2"
	DefaultDeepgramLang    = "zh-TW"
)

type Settings struct {
	AppName      string
	RunEnv       string
	DebugEnabled bool
}

type Config struct {
	APIKeys APIKeysConfig `yaml:"api_keys" json:"-"`
	LLM     LLMConfig     `yaml:"llm" json:"llm"`
	STT     STTConfig     `yaml:"stt" json:"stt"`
	TTS     TTSConfig     `yaml:"tts" json:"tts"`
}

type APIKeysConfig struct {
	OpenAI   string `yaml:"openai" json:"-"`
	Deepgram string `yaml:"deepgram" json:"-"`
	Cartesia string `yaml:"cartesia" json:"-"`
}

type LLMConfig struct {
	Engine string       `yaml:"engine" json:"engine"`
	OpenAI OpenAIConfig `yaml:"openai" json:"openai"`
}

type OpenAIConfig struct {
	Model           string  `yaml:"model" json:"model"`
	MaxOutputTokens int     `yaml:"max_output_tokens" json:"max_output_tokens"`
	Temperature     float64 `yaml:"temperature" json:"temperature"`
}

type STTConfig struct {
	Engine   string         `yaml:"engine" json:"engine"`
	Deepgram DeepgramConfig `yaml:"deepgram" json:"deepgram"`
}

type DeepgramConfig struct {
	Model          string `yaml:"model" json:"model"`
	Language       string `yaml:"language" json:"language"`
	SmartFormat    bool   `yaml:"smart_format" json:"smart_format"`
	InterimResults bool   `yaml:"interim_results" json:"interim_results"`
	VadEvents      bool   `yaml:"vad_events" json:"vad_events"`
	EndpointingMs  int    `yaml:"endpointing_ms" json:"endpointing_ms"`
	UtteranceEndMs int    `yaml:"utterance_end_ms" json:"utterance_end_ms"`
}

type TTSConfig struct {
	Engine   string         `yaml:"engine" json:"engine"`
	Cartesia CartesiaConfig `yaml:"cartesia" json:"cartesia"`
}

type CartesiaConfig struct {
	VoiceID    string `yaml:"voice_id" json:"voice_id"`
	ModelID    string `yaml:"model_id" json:"model_id"`
	Language   string `yaml:"language" json:"language"`
	SampleRate int    `yaml:"sample_rate" json:"sample_rate"`
	APIVersion string `yaml:"api_version" json:"api_version"`
}

func DefaultSettings() Settings {
	return Settings{
		AppName:      "Miko",
		RunEnv:       "development",
		DebugEnabled: true,
	}
}

func DefaultConfig() Config {
	return Config{
		LLM: LLMConfig{
			Engine: "openai",
			OpenAI: OpenAIConfig{
				Model:           DefaultOpenAIModel,
				MaxOutputTokens: 250,
				Temperature:     0.7,
			},
		},
		STT: STTConfig{
			Engine: "deepgram",
			Deepgram: DeepgramConfig{
				Model:          DefaultDeepgramModel,
				Language:       DefaultDeepgramLang,
				SmartFormat:    true,
				InterimResults: true,
				VadEvents:      true,
				EndpointingMs:  300,
				UtteranceEndMs: 1200,
			},
		},
		TTS: TTSConfig{
			Engine: "cartesia",
			Cartesia: CartesiaConfig{
				VoiceID:    DefaultCartesiaVoiceID,
				ModelID:    DefaultCartesiaModelID,
				Language:   "zh",
				SampleRate: 16000,
				APIVersion: DefaultCartesiaVersion,
			},
		},
	}
}

func LoadSettings() Settings {
	settings := DefaultSettings()
	settings.RunEnv = getEnv("RUN_ENV", settings.RunEnv)
	settings.DebugEnabled = getEnvBool("DEBUG_ENABLED", settings.DebugEnabled)
	return settings
}

func LoadConfig() (Config, string, error) {
	cfg := DefaultConfig()
	path, err := userConfigPath()
	if err != nil {
		return cfg, "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, path, nil
		}
		return cfg, path, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, path, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return cfg, path, err
	}
	return cfg, path, nil
}

func EnsureConfigDir() (string, error) {
	path, err := userConfigPath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func (c Config) Validate() error {
	if c.LLM.Engine != "openai" {
		return fmt.Errorf("unsupported llm.engine %q", c.LLM.Engine)
	}
	if c.STT.Engine != "deepgram" {
		return fmt.Errorf("unsupported stt.engine %q", c.STT.Engine)
	}
	if c.TTS.Engine != "cartesia" {
		return fmt.Errorf("unsupported tts.engine %q", c.TTS.Engine)
	}
	if c.LLM.OpenAI.Model == "" {
		c.LLM.OpenAI.Model = DefaultOpenAIModel
	}
	if c.TTS.Cartesia.VoiceID == "" {
		c.TTS.Cartesia.VoiceID = DefaultCartesiaVoiceID
	}
	if c.TTS.Cartesia.ModelID == "" {
		c.TTS.Cartesia.ModelID = DefaultCartesiaModelID
	}
	if c.TTS.Cartesia.APIVersion == "" {
		c.TTS.Cartesia.APIVersion = DefaultCartesiaVersion
	}
	if c.TTS.Cartesia.SampleRate == 0 {
		c.TTS.Cartesia.SampleRate = 16000
	}
	if c.STT.Deepgram.Model == "" {
		c.STT.Deepgram.Model = DefaultDeepgramModel
	}
	if c.STT.Deepgram.Language == "" {
		c.STT.Deepgram.Language = DefaultDeepgramLang
	}
	return nil
}

func userConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".miko", "config.yaml"), nil
}

func getEnv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
