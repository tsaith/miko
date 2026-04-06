package main

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"miko/internal/assistant"
	"miko/internal/config"
	"miko/internal/debuglog"
)

type App struct {
	ctx       context.Context
	assistant *assistant.Service
	logger    *debuglog.Logger
	state     config.RuntimeState
}

func NewApp() *App {
	rand.Seed(time.Now().UnixNano())
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	settings := config.LoadSettings()
	cfg, cfgPath, err := config.LoadConfig()
	if err != nil {
		runtime.LogErrorf(ctx, "load config failed: %v", err)
		cfg = config.DefaultConfig()
	}
	logger, logErr := debuglog.New(settings.DebugEnabled)
	if logErr != nil {
		runtime.LogErrorf(ctx, "init logger failed: %v", logErr)
	} else {
		a.logger = logger
		logger.Infof("app", "startup config=%s debug=%t", cfgPath, settings.DebugEnabled)
	}

	service, err := assistant.NewService(settings, cfg, cfgPath, func(event string, payload any) {
		if a.ctx == nil {
			return
		}
		runtime.EventsEmit(a.ctx, event, payload)
	}, logger)
	if err != nil {
		runtime.LogErrorf(ctx, "assistant init failed: %v", err)
		if logger != nil {
			logger.Errorf("app", "assistant init failed: %v", err)
		}
		a.state = config.RuntimeState{
			AppName:      settings.AppName,
			ConfigPath:   cfgPath,
			LogPath:      logPath(logger),
			LogDirectory: logDir(logger),
			Config:       cfg,
			PlatformNote: fmt.Sprintf("初始化音訊失敗: %v", err),
		}
		return
	}

	a.assistant = service
	a.state = service.BootstrapState()
}

func (a *App) shutdown(context.Context) {
	if a.assistant != nil {
		a.assistant.Close()
	}
	if a.logger != nil {
		a.logger.Info("app", "shutdown")
		_ = a.logger.Close()
	}
}

func (a *App) StartListening() error {
	if a.assistant == nil {
		return fmt.Errorf("assistant service is not ready")
	}
	return a.assistant.StartListening()
}

func (a *App) StopListening() error {
	if a.assistant == nil {
		return nil
	}
	return a.assistant.StopListening()
}

func (a *App) GetRuntimeState() config.RuntimeState {
	return a.state
}

func logPath(logger *debuglog.Logger) string {
	if logger == nil {
		return ""
	}
	return logger.Path()
}

func logDir(logger *debuglog.Logger) string {
	if logger == nil {
		return ""
	}
	return logger.Directory()
}
