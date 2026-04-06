# Miko — CLAUDE.md

Miko 是一個桌上型語音智能助理，使用 Wails 封裝 Go 後端與 React 前端，並透過 VRM 顯示 bust view Avatar。

## 實際技術架構

- 桌面殼：Wails
- 後端：Go
- 前端：TypeScript + React
- 3D Avatar：Three.js + `@pixiv/three-vrm`
- 雲端服務：
  - Deepgram STT
  - OpenAI LLM
  - Cartesia TTS
- OpenAI SDK：
  - 官方 `github.com/openai/openai-go/v3`

## 不支援的內容

- 不支援本地 Whisper / Qwen / Kokoro / MLX 模型
- 不支援瀏覽器端錄音與瀏覽器端 TTS 播放
- 不使用 FastAPI 或 WebSocket

## 專案結構

```text
miko/
  main.go
  app.go
  internal/
    assistant/service.go
    audio/service.go
    config/
      config.go
      runtime.go
    debuglog/logger.go
    avatar/
      motion_controller.go
      pose_manager.go
      expression_manager.go
      types.go
    services/
      brain/brain.go
      llm/openai.go
      stt/deepgram.go
      tts/cartesia.go
  frontend/
    src/
      App.tsx
      App.css
      avatar/
        avatar-manager.ts
        scene-manager.ts
        expression-manager.ts
        pose-manager.ts
        caption-manager.ts
        motion-types.ts
    public/
      vrm/
        Yuna.vrm
        Yuki.vrm
  config_files/config.yaml.example
```

## Runtime Model

Miko 採用後端掌控音訊裝置的桌面助理架構：

- 後端負責：
  - 麥克風收音
  - 喇叭播放
  - Deepgram STT
  - OpenAI 回覆生成
  - Cartesia TTS
  - Avatar motion frame 生成
- 前端負責：
  - VRM 顯示
  - chat UI
  - 字幕
  - listening / thinking / VAD 狀態
  - 右側聊天面板內部捲動

## Event Protocol

前端呼叫 Go methods：

- `StartListening()`
- `StopListening()`
- `GetRuntimeState()`

後端透過 Wails events 推送：

- `listening_state`
- `vad_status`
- `chat_update`
- `chat_status`
- `tts_start`
- `tts_end`
- `avatar_motion`
- `app_error`

## 設定檔

使用者設定檔位於：

```text
~/.miko/config.yaml
```

API keys 定義在這個檔案裡：

```yaml
api_keys:
  openai: YOUR_OPENAI_API_KEY
  deepgram: YOUR_DEEPGRAM_API_KEY
  cartesia: YOUR_CARTESIA_API_KEY
```

這些欄位只提供後端使用，不會序列化到前端。

Miko 啟動時也會自動建立：

```text
~/.miko/workspace/
```

## Debug Log

後端會自動將 debug log 寫到：

```text
~/.miko/workspace/logs/
```

啟動時會自動清理超過 5 天的 `miko-YYYY-MM-DD.log`。

每次對話都會帶 session id，並記錄：

- listening start / stop
- Deepgram utterance lifecycle
- Deepgram KeepAlive / reconnect lifecycle
- OpenAI request timing
- Cartesia request timing
- audio playback backend 與錯誤

## 主要可設定欄位

- OpenAI model
- Deepgram model / language / endpointing
- Cartesia voice_id / model_id / sample_rate / api_version

Cartesia `voice_id` 的 fallback 為：

```text
6eb8965c-e295-47bd-a9e4-3eeebb3abcff
```

## 開發命令

```bash
wails dev
wails build
```

如需先安裝前端依賴：

```bash
cd frontend
npm install
```

## 平台目標

- macOS
- Linux ARM64

這包含 Raspberry Pi 類型的 ARM64 Linux 裝置，但仍需要目標系統本身具備可用的音訊輸入輸出裝置與對應驅動。
