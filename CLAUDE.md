# Miko — CLAUDE.md

Miko 的目標架構是：

- Go + Wails 作為桌面 shell
- Python 3.12 + gRPC + OpenCV + OpenAI 作為 backend sidecar
- React + Three.js + VRM 作為前端 UI

## 主要原則

1. 音訊裝置保留在 Go
2. Python backend 採 sidecar 形式
3. 人臉偵測只做 face center
4. 前端維持 React + Three.js + VRM

## 技術切分

### Go / Wails

負責：

- 桌面應用程式生命週期
- 前端橋接
- 麥克風收音
- 喇叭播放
- 啟動與監控 Python backend
- gRPC client
- 播放 sidecar 回傳的語音
- macOS / Linux 應用程式建置與打包

Go 端不再提供 STT / LLM / TTS 的主流程。

### Python backend

放在 `backend/`，使用：

- Python 3.12
- `uv`
- `grpcio`
- OpenCV
- OpenAI

負責：

- face center 偵測
- STT
- LLM
- TTS
- 對話流程
- face target 資料

### Frontend

負責：

- VRM 呈現
- 聊天 UI
- 字幕
- API 狀態
- 交談控制

## 建議專案結構

```text
miko/
  main.go
  app.go
  internal/
    audio/
    backendclient/
    config/
    debuglog/
    runtime/
  frontend/
  proto/
    assistant.proto
  backend/
    pyproject.toml
    uv.lock
    app/
      server.py
      config.py
      pipeline/
      services/
        vision/
        stt/
        llm/
        tts/
```

## 通訊模型

- Frontend <-> Go：
  - Wails methods
  - Wails events
- Go <-> Python：
  - gRPC over Unix socket

建議的 gRPC 能力包含：

- health check
- session control
- audio streaming
- transcript events
- tts audio streaming
- face target events
- avatar motion events

## 設定與 Workspace

設定檔：

```text
~/.miko/config.yaml
```

Workspace：

```text
~/.miko/workspace/
~/.miko/workspace/logs/
```

目前 sidecar 的 STT / LLM / TTS debug log 也會透過 Go 收進同一份 log。

## 預設模型

- [frontend/public/vrm/Yuna.vrm](/Users/andrew/projects/miko/frontend/public/vrm/Yuna.vrm)

## 開發流程

Go / Wails：

```bash
wails dev
```

Frontend：

```bash
cd frontend
npm install
npm run build
```

Python backend：

```bash
cd backend
uv sync
uv run python -m app.server
```

## 發佈模型

正式發佈時採用：

- Go / Wails 主程式
- Python backend sidecar

Go 的責任是產出桌面應用程式，並在 runtime 啟動 sidecar。
