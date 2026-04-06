# Miko

Miko 是一個基於 Wails 的桌上型語音智能助理。使用者可以直接用語音與助理交談，並在介面中看到 VRM bust view Avatar。

技術堆疊：

- 後端：Go
- 桌面框架：Wails
- 前端：TypeScript + React + Three.js + `@pixiv/three-vrm`
- 雲端服務：
  - STT：Deepgram
  - LLM：OpenAI
  - TTS：Cartesia

## 專案重點

- 只支援雲端 `deepgram`、`openai`、`cartesia`
- 不支援任何本地 STT、LLM、TTS 模型
- OpenAI 整合使用官方 Go SDK `github.com/openai/openai-go/v3`
- 前端不直接錄音、不直接播放 TTS 音訊
- 前後端透過 Wails method binding + Wails events 溝通，不使用 WebSocket
- 預設 VRM 模型為 [frontend/public/vrm/Yuna.vrm](/Users/andrew/projects/miko/frontend/public/vrm/Yuna.vrm)
- 其他 VRM 素材也統一放在 [frontend/public/vrm](/Users/andrew/projects/miko/frontend/public/vrm)

## 目錄

```text
miko/
  app.go
  main.go
  internal/
    assistant/
    audio/
    avatar/
    config/
    debuglog/
    services/
      brain/
      llm/
      stt/
      tts/
  frontend/
    public/
      vrm/
        Yuna.vrm
        Yuki.vrm
    src/
      App.tsx
      App.css
      avatar/
  config_files/
    config.yaml.example
```

## 設定

1. 建立使用者設定檔

```bash
mkdir -p ~/.miko
cp config_files/config.yaml.example ~/.miko/config.yaml
```

2. 在 `~/.miko/config.yaml` 填入 API keys

```yaml
api_keys:
  openai: YOUR_OPENAI_API_KEY
  deepgram: YOUR_DEEPGRAM_API_KEY
  cartesia: YOUR_CARTESIA_API_KEY
```

API key 只從 `~/.miko/config.yaml` 讀取，不從 `.env` 讀取。

## Workspace

Miko 啟動時會自動確認並建立：

```text
~/.miko/workspace/
```

目前 workspace 主要用於：

- `~/.miko/workspace/logs/`

## 開發

```bash
wails dev
```

Wails 會啟動 Go 後端與前端開發環境。

如需先安裝前端依賴：

```bash
cd frontend
npm install
```

## 建置

```bash
wails build
```

## Debug Log

後端會自動把 debug log 寫到：

```text
~/.miko/workspace/logs/
```

系統只保留最近 5 天的 `miko-YYYY-MM-DD.log`，啟動時會自動清理更舊的檔案。

目前會記錄：

- app startup / shutdown
- 每次對話的 session id
- Deepgram 連線、KeepAlive、斷句與重連
- OpenAI 請求耗時
- Cartesia 請求耗時與回傳大小
- 音訊播放 backend 與播放錯誤

log 不會寫入 API key。

## 執行模型

- 後端負責：
  - 本機麥克風收音
  - 本機喇叭播放
  - Deepgram STT
  - OpenAI LLM
  - Cartesia TTS
  - Avatar motion frame 生成
- 前端負責：
  - VRM 渲染
  - chat UI
  - 字幕
  - listening / thinking / VAD 狀態
  - 右側聊天面板內部捲動

## 平台支援

- macOS
- Linux ARM64，例如 Raspberry Pi

音訊 I/O 使用 Go 的本機音訊裝置層，實際部署時仍需目標系統提供可用的輸入與輸出裝置。
