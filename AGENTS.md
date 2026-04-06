# Miko Agent Architecture

這份文件描述 Miko 專案目前的桌面助理架構、語音管線與前後端責任切分。

## 整體架構

Miko 是基於 **Wails + Go + React** 的桌上型語音智能助理。

- **後端 (Go)**：負責本機麥克風收音、喇叭播放、雲端 STT / LLM / TTS 呼叫、Avatar motion runtime
- **前端 (React / Three.js / VRM)**：負責 UI 呈現、VRM bust view 渲染、聊天紀錄、字幕與狀態顯示

前端不直接使用瀏覽器麥克風 API，也不直接播放 TTS 音訊。

## 支援的服務

本專案只支援以下雲端服務：

- **Deepgram STT**
- **OpenAI LLM**
- **Cartesia TTS**

補充：

- OpenAI 使用官方 `openai-go/v3`
- 不支援任何本地 STT、LLM、TTS 模型

## 語音互動流程

1. 後端啟動本機麥克風收音
2. 音訊以 `16kHz / mono / Int16 PCM` 送往 Deepgram live transcription
3. 後端以簡單 RMS 門檻回推 `vad_status`，供前端顯示說話狀態
4. Deepgram 連線會定期送 KeepAlive，若意外中斷會嘗試自動重連
5. Deepgram 回傳完整語句後，後端送入 OpenAI 產生回覆
6. 後端先推送 `chat_update` 與 `chat_status`
7. 回覆文字送往 Cartesia 產生 PCM
8. 後端直接播放 PCM 到本機喇叭
9. 後端依播放中的音量更新嘴型，並持續推送 `avatar_motion`

## 前後端通訊

Miko 不使用 FastAPI WebSocket。

改用 Wails 提供的兩種通道：

- **Method binding**
  - `StartListening()`
  - `StopListening()`
  - `GetRuntimeState()`
- **Wails events**
  - `listening_state`
  - `vad_status`
  - `chat_update`
  - `chat_status`
  - `tts_start`
  - `tts_end`
  - `avatar_motion`
  - `app_error`

## 後端模組

### 入口

- [main.go](/Users/andrew/projects/miko/main.go)
- [app.go](/Users/andrew/projects/miko/app.go)

### Orchestration

- [internal/assistant/service.go](/Users/andrew/projects/miko/internal/assistant/service.go)

負責串接：

- 麥克風收音
- Deepgram STT
- OpenAI LLM
- Cartesia TTS
- Avatar motion events

### 設定與 Workspace

- [internal/config/config.go](/Users/andrew/projects/miko/internal/config/config.go)
- [internal/config/runtime.go](/Users/andrew/projects/miko/internal/config/runtime.go)

來源：

- `~/.miko/config.yaml`

Workspace：

- `~/.miko/workspace/` 會在啟動時自動建立
- log 會寫到 `~/.miko/workspace/logs/`

API keys 只放在 `~/.miko/config.yaml` 的 `api_keys` 區塊，且不會回傳到前端。

### Debug Log

- [internal/debuglog/logger.go](/Users/andrew/projects/miko/internal/debuglog/logger.go)

特性：

- 自動每日切 log
- 只保留最近 5 天
- 記錄 session id
- 記錄 STT / LLM / TTS / audio playback 生命週期

### 音訊 I/O

- [internal/audio/service.go](/Users/andrew/projects/miko/internal/audio/service.go)

負責：

- 啟動本機麥克風
- 播放 TTS PCM
- 計算 RMS
- 系統播放器優先，必要時 fallback 到 `malgo`

### AI 服務

- [internal/services/stt/deepgram.go](/Users/andrew/projects/miko/internal/services/stt/deepgram.go)
- [internal/services/llm/openai.go](/Users/andrew/projects/miko/internal/services/llm/openai.go)
- [internal/services/tts/cartesia.go](/Users/andrew/projects/miko/internal/services/tts/cartesia.go)
- [internal/services/brain/brain.go](/Users/andrew/projects/miko/internal/services/brain/brain.go)

## Avatar

### 後端 motion runtime

- [internal/avatar/motion_controller.go](/Users/andrew/projects/miko/internal/avatar/motion_controller.go)
- [internal/avatar/pose_manager.go](/Users/andrew/projects/miko/internal/avatar/pose_manager.go)
- [internal/avatar/expression_manager.go](/Users/andrew/projects/miko/internal/avatar/expression_manager.go)
- [internal/avatar/types.go](/Users/andrew/projects/miko/internal/avatar/types.go)

### 前端 VRM 套用層

- [frontend/src/App.tsx](/Users/andrew/projects/miko/frontend/src/App.tsx)
- [frontend/src/avatar/avatar-manager.ts](/Users/andrew/projects/miko/frontend/src/avatar/avatar-manager.ts)
- [frontend/src/avatar/scene-manager.ts](/Users/andrew/projects/miko/frontend/src/avatar/scene-manager.ts)
- [frontend/src/avatar/expression-manager.ts](/Users/andrew/projects/miko/frontend/src/avatar/expression-manager.ts)
- [frontend/src/avatar/pose-manager.ts](/Users/andrew/projects/miko/frontend/src/avatar/pose-manager.ts)
- [frontend/src/avatar/caption-manager.ts](/Users/andrew/projects/miko/frontend/src/avatar/caption-manager.ts)

## 預設模型

前端預設載入：

- [frontend/public/vrm/Yuna.vrm](/Users/andrew/projects/miko/frontend/public/vrm/Yuna.vrm)

其他可用素材：

- [frontend/public/vrm/Yuki.vrm](/Users/andrew/projects/miko/frontend/public/vrm/Yuki.vrm)

## UI 版面約束

目前介面採固定視窗高度的 Grid/Flex 版面：

- 左側為 Avatar bust view
- 右側為控制面板與聊天紀錄
- 只有聊天容器本身允許捲動

前端避免使用會把整個頁面一起捲動的 `scrollIntoView()`，改為只控制聊天容器本身的捲動位置。

## 平台目標

目前目標平台：

- macOS
- Linux ARM64

例如 Raspberry Pi。
