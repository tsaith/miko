# Miko Agent Architecture

這份文件描述 Miko 專案的目標架構與模組責任切分。

## 整體方向

Miko 採用雙程序桌面架構：

- **Go + Wails shell**
  - 建立桌面視窗
  - 管理前端
  - 控制本機音訊裝置
  - 啟動與監控 Python backend sidecar
  - 透過 Unix socket 上的 gRPC 與 sidecar 通訊
- **Python backend sidecar**
  - 使用 Python 3.12
  - 使用 `uv` 管理套件
  - 提供電腦視覺、STT、LLM、TTS、motion planning 等服務
- **React / Three.js / VRM frontend**
  - 顯示 VRM bust view
  - 顯示聊天紀錄與字幕
  - 顯示 listening / thinking / speaking 狀態

## 責任切分

### Go shell

Go 端負責：

- Wails app lifecycle
- 前端事件橋接
- 本機麥克風收音
- 本機喇叭播放
- 啟動 / 停止 Python backend
- gRPC client
- 將 backend 回傳的狀態轉成前端可消費的 Wails events
- 播放 sidecar 回傳的 TTS PCM
- 打包成可在 macOS / Linux 執行的桌面應用程式

音訊裝置必須留在 Go，不交給 Python。
Go 端不再提供 STT / LLM / TTS 的主流程。

### Python backend

Python backend 放在 `backend/`，使用：

- Python 3.12
- `uv`
- `grpcio`
- `opencv-python`
- `openai`
- `deepgram`
- `cartesia`

Python 端負責：

- OpenCV face center 偵測
- STT
- LLM
- TTS
- 對話狀態管理
- face target 資料生成

### Frontend

前端維持：

- React
- Three.js
- `@pixiv/three-vrm`

前端不直接碰麥克風與喇叭裝置。

## 通訊模型

### Frontend <-> Go

透過 Wails methods + events：

- `StartListening()`
- `StopListening()`
- `GetRuntimeState()`

事件包含：

- `listening_state`
- `vad_status`
- `chat_update`
- `chat_status`
- `tts_start`
- `tts_end`
- `avatar_motion`
- `app_error`

### Go <-> Python backend

透過 Unix socket 上的 gRPC。

建議的 proto 方向：

- `HealthService`
- `ConversationService`
- `VisionService`

建議的事件流內容：

- transcript partial / final
- chat update
- tts audio chunk
- tts lifecycle
- face target
- avatar motion
- app error

## 建議目錄

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

## 模型與 UI

- 預設 VRM 模型：
  - [frontend/public/vrm/Yuna.vrm](/Users/andrew/projects/miko/frontend/public/vrm/Yuna.vrm)
- 其他 VRM 素材：
  - [frontend/public/vrm/Yuki.vrm](/Users/andrew/projects/miko/frontend/public/vrm/Yuki.vrm)

目前 UI 的方向：

- 左側：Avatar bust view
- 右側：聊天紀錄與交談控制
- 保留 API ready / missing 狀態

## 設定與 Workspace

使用者設定檔：

- `~/.miko/config.yaml`

Workspace：

- `~/.miko/workspace/`
- `~/.miko/workspace/logs/`

API keys 存放在 `~/.miko/config.yaml` 的 `api_keys` 區塊。

## Log 現況

目前 runtime log 會統一寫到 `~/.miko/workspace/logs/`。

內容包含：

- Go shell / Wails / audio / avatar log
- Python sidecar stdout / stderr
- sidecar 的 STT / LLM / TTS 關鍵事件與耗時

## 發佈策略

正式發佈時採用 sidecar 形式：

- Go / Wails 主程式
- Python backend sidecar

也就是說，桌面應用程式不是把 Python 邏輯重寫進 Go，而是由 Go 負責整合與啟動 sidecar。

## 平台目標

- macOS
- Linux
- Linux ARM64，例如 Raspberry Pi
