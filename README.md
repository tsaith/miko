# Miko

Miko 是一個桌上型語音智能助理。UI 維持 React + Three.js + VRM，桌面殼使用 Wails，語音與 AI 後端由 Python sidecar 提供。

## 目標架構

- 桌面殼：Go + Wails
- 前端：TypeScript + React + Three.js + `@pixiv/three-vrm`
- AI / CV 後端：Python 3.12
- 後端通訊：gRPC over Unix socket
- Python 套件管理：`uv`

## 核心設計

- Go 負責：
  - 建立 macOS / Linux 桌面執行檔
  - 啟動與監控 Python backend sidecar
  - 本機麥克風收音
  - 本機喇叭播放
  - 將 backend 事件轉發給前端
  - 播放 Python sidecar 回傳的 TTS PCM
- Python backend 負責：
  - OpenCV 人臉偵測
  - STT
  - LLM
  - TTS
  - 對話流程管理
- 前端負責：
  - VRM Avatar 渲染
  - 聊天紀錄
  - 字幕
  - API ready / missing 狀態
  - listening / thinking / speaking 等 UI 狀態

## 通訊方式

- Frontend <-> Go：Wails method bindings + Wails events
- Go <-> Python backend：gRPC over Unix socket

Python sidecar 的 IPC 固定使用 Unix socket。

建議的 Python sidecar 啟動方式：

- 開發模式：Go 啟動 `uv run python -m app.server`
- 發佈模式：Go 啟動隨應用程式一起分發的 Python backend sidecar

## 目錄方向

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
    public/
      vrm/
        Yuna.vrm
        Yuki.vrm
    src/
      App.tsx
      App.css
      avatar/
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
  config_files/
    config.yaml.example
```

## 後端服務邊界

Python backend 的服務目標：

- `vision`
  - 使用 OpenCV
  - 只做 face center 偵測
- `stt`
  - Deepgram
  - 目前由 sidecar 先做分段，再送雲端辨識
- `llm`
  - OpenAI
- `tts`
  - Cartesia

Go 端保留音訊 I/O，不把麥克風與喇叭控制移到 Python。Go 本身不再提供 STT / LLM / TTS 實作。

## 設定

使用者設定檔位於：

```text
~/.miko/config.yaml
```

API key 只從這個檔案讀取：

```yaml
api_keys:
  openai: YOUR_OPENAI_API_KEY
  deepgram: YOUR_DEEPGRAM_API_KEY
  cartesia: YOUR_CARTESIA_API_KEY
```

Miko 啟動時會自動確認並建立：

```text
~/.miko/workspace/
~/.miko/workspace/logs/
```

## 開發

Go / Wails：

```bash
wails dev
```

前端：

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

## 建置

Go 的責任是產出桌面殼與整合 sidecar 的應用程式：

```bash
wails build
```

未來正式發佈時，會採用：

- Go / Wails 主程式
- Python backend sidecar
- sidecar 隨應用程式一同分發

## Debug Log

應用程式 log 會寫到：

```text
~/.miko/workspace/logs/
```

系統只保留最近 5 天的 `miko-YYYY-MM-DD.log`。

目前同一份 log 會包含：

- Go / Wails shell log
- Python sidecar stdout / stderr
- sidecar 的 STT / LLM / TTS 關鍵事件與耗時

## 平台目標

- macOS
- Linux

其中 Linux 目標包含 Linux ARM64，例如 Raspberry Pi。
