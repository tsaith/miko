# Echo Agent Architecture

這份文件描述 Echo 專案目前的代理、語音處理與前後端責任切分。

## 目前的整體方向

Echo 現在朝「本機桌面助理」的架構演進：

- **後端 (go)**：負責本機麥克風收音、喇叭播放、VAD、STT、LLM、TTS、Avatar motion。
- **前端 (React / Three.js / VRM)**：只負責 UI 呈現、Avatar 渲染、字幕與聊天紀錄展示，以及透過 WebSocket 發送控制指令。

也就是說，前端不再直接處理 `getUserMedia`、`AudioWorklet` 或瀏覽器端 TTS 播放。

## 語音處理管線 (Audio Pipeline)

Echo 的語音互動流程如下：

1. **本機麥克風收音 (Local Audio Capture)**:
   - 由後端 `LocalAudioService` 直接透過 `PyAudio` 開啟本機麥克風。
   - 收音格式統一為 **16kHz / mono / Int16 PCM**。
   - 收到的音訊會透過 `asyncio.Queue` 送入每個 session 的語音處理流程。

2. **VAD (Voice Activity Detection)**:
   - 使用 `silero-vad`。
   - 後端維護每個 session 的 `audio_buffer`，並依據 `VADService.BUFFER_SIZE = 512` 進行安全切片。
   - 每一段音訊都會回推出 `vad_status` 給前端，供 UI 顯示目前是否在說話。

3. **STT (Speech to Text)**:
   - 由 `STTService` 作為入口統一切換不同辨識引擎。
   - 目前支援：
     - **Deepgram STT**：雲端即時辨識。
     - **MLX Whisper STT**：本地離線辨識。
     - **MLX Qwen3 STT**：本地離線辨識。
   - 本地 STT 服務會依後端 VAD 的 speech / silence 狀態做斷句，累積音訊後再送入模型。

4. **LLM (Language Agent)**:
   - 由 `BrainService` 管理對話狀態與上下文。
   - `LLMService` 可切換：
     - **OpenAI LLM**
     - **MLX Qwen3 LLM**
   - LLM 回覆完成後，會先推送 `chat_update` 到前端，再啟動 TTS。

5. **TTS (Text to Speech)**:
   - 由 `TTSService` 統一管理。
   - 目前支援：
     - **Cartesia TTS**：雲端即時生成。
     - **Kokoro TTS**：本地生成。
   - 後端拿到 TTS PCM chunk 後，會：
     - 直接在本機喇叭播放
     - 計算音量 RMS 用來驅動 Avatar 嘴形
     - 推送 `tts_start` / `tts_end` 供前端控制字幕顯示

## 核心元件

### 後端入口

- **`main.py`**：
  - FastAPI 原生 WebSocket 入口
  - 建立每個 session 的 `STTService`、`BrainService`、`MotionController`
  - 啟停本機收音
  - 啟動 Avatar motion loop
  - 協調 STT → LLM → TTS 的整條流程

### 音訊 I/O

- **`app/services/local_audio_service.py`**：
  - 直接控制本機音訊裝置
  - 提供：
    - `start_capture()`：啟動麥克風收音
    - `stop_capture()`：停止麥克風
    - `play_audio()`：將 TTS PCM 資料送到本機喇叭

### 語音與 AI 服務

- **`app/services/vad_service.py`**：Silero VAD
- **`app/services/stt_service.py`**：STT 路由器
- **`app/services/llm_service.py`**：LLM 路由器
- **`app/services/tts_service.py`**：TTS 路由器
- **`app/services/brain_service.py`**：對話狀態與回應生成

### Avatar Motion

- **`app/lib/avatar/motion_controller.py`**：
  - 後端 motion runtime 的總入口
  - 組合：
    - `pose_manager.py`
    - `expression_manager.py`
    - `motion_types.py`

後端每秒固定推送 motion frame 給前端，前端只負責把 frame 套用到 VRM。

## 前後端整合架構

### 前端責任

前端目前只做以下工作：

- 與後端建立 WebSocket
- 顯示聊天內容、thinking 狀態、字幕、VAD orb
- 顯示與更新 VRM Avatar
- 發送控制事件：
  - `start_listen`
  - `stop_listen`

### 後端責任

後端負責：

- 本機麥克風收音
- 本機喇叭播放
- 音訊格式統一
- VAD / STT / LLM / TTS
- Avatar motion 計算
- 將狀態事件推送到前端

## WebSocket 事件協定

### Frontend → Backend

- `{"event": "start_listen"}`：開始交談
- `{"event": "stop_listen"}`：停止交談

前端不再傳送任何二進位 PCM 音訊。

### Backend → Frontend

- `listening_state`：`{ is_listening: boolean }`
- `vad_status`：`{ is_speech: boolean }`
- `chat_update`：`{ role, content }`
- `chat_status`：`{ status: "idle" | "thinking" }`
- `tts_start`：TTS 播放開始
- `tts_end`：TTS 播放結束
- `avatar_motion`：Avatar 骨骼 / 表情運動 frame

## 前端 3D 虛擬角色架構 (VRM Avatar)

Echo 前端透過 `@pixiv/three-vrm` 與 `three.js` 渲染 VRM 角色。

### 元件設計

- **`SceneManager`** (`ui/src/avatar/scene-manager.ts`)
  - Three.js 場景生命週期、相機、光源、RAF loop、resize 處理
- **`AvatarManager`** (`ui/src/avatar/avatar-manager.ts`)
  - 載入 VRM，協調表情與姿勢套用
- **`ExpressionManager`** (`ui/src/avatar/expression-manager.ts`)
  - 套用後端送來的表情 frame
- **`PoseManager`** (`ui/src/avatar/pose-manager.ts`)
  - 套用後端送來的骨骼姿勢與風場資料

### 嘴形同步流程 (Lip Sync)

1. 後端 TTS 產生 PCM chunk。
2. 後端根據 chunk 計算 RMS / mouth-open 強度。
3. 後端 `MotionController` 更新 `aa` 嘴形目標值。
4. 後端透過 `avatar_motion` 事件把表情 frame 推送給前端。
5. 前端只負責將該數值套用到 VRM morph。

## UI 佈局

- 左側：Three.js / VRM Avatar 全高畫面
- 右側：聊天紀錄面板與「開始交談 / 停止交談」按鈕
- 左下角：VAD 狀態燈
- Avatar 上方：字幕框與 thinking 動畫

## 對目前架構的理解重點

1. Echo 已不是瀏覽器端語音助理，而是**後端掌控本機音訊裝置**的助理。
2. 前端只是控制面板與 Avatar 視覺層。
3. 若未來做桌面封裝，這個責任切分可直接延伸到 `FastAPI + Desktop Shell` 的架構。
