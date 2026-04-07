package main

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"miko/internal/assistant"
	"miko/internal/backendclient"
	"miko/internal/config"
	"miko/internal/debuglog"
)

type App struct {
	ctx       context.Context
	assistant *assistant.Service
	backend   *backendclient.Manager
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

	if cfg.Backend.Enabled {
		a.backend = backendclient.NewManager(cfg.Backend, logger)
		if err := a.backend.Start(ctx); err != nil {
			runtime.LogWarningf(ctx, "python backend sidecar start failed: %v", err)
			if logger != nil {
				logger.Warnf("app", "python backend sidecar start failed: %v", err)
			}
		}
	}

	service, err := assistant.NewService(settings, cfg, cfgPath, a.backend, func(event string, payload any) {
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
			Backend:      backendStatus(a.backend, cfg.Backend),
			PlatformNote: fmt.Sprintf("初始化音訊失敗: %v", err),
		}
		return
	}

	a.assistant = service
	a.state = service.BootstrapState()
	a.state.Backend = backendStatus(a.backend, cfg.Backend)

	if a.backend != nil && a.backend.Status().Running {
		if err := a.backend.StartVisionStream(ctx, service.SetFaceTarget); err != nil {
			runtime.LogWarningf(ctx, "python backend vision stream start failed: %v", err)
			if logger != nil {
				logger.Warnf("app", "python backend vision stream start failed: %v", err)
			}
		}
	}
}

func (a *App) shutdown(context.Context) {
	if a.backend != nil {
		a.backend.Close()
	}
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
	if a.backend != nil {
		a.state.Backend = a.backend.Status()
	}
	return a.state
}

func (a *App) PingBackend() (config.BackendStatus, error) {
	if a.backend == nil {
		return backendStatus(nil, config.BackendConfig{}), fmt.Errorf("backend sidecar is not enabled")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := a.backend.Ping(ctx); err != nil {
		a.state.Backend = a.backend.Status()
		return a.state.Backend, err
	}

	a.state.Backend = a.backend.Status()
	return a.state.Backend, nil
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

func backendStatus(manager *backendclient.Manager, cfg config.BackendConfig) config.BackendStatus {
	if manager == nil {
		endpoint := ""
		if cfg.SocketPath != "" {
			endpoint = "unix://" + cfg.SocketPath
		}
		return config.BackendStatus{
			Enabled:   cfg.Enabled,
			Running:   false,
			Mode:      cfg.LaunchMode,
			Transport: "unix",
			Endpoint:  endpoint,
		}
	}
	return manager.Status()
}
