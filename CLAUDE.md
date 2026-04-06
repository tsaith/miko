# Echo — CLAUDE.md

Personal AI voice assistant with local microphone capture, local speaker playback, STT, LLM, TTS, and a 3D VRM avatar UI.

## Project Layout

```text
echo/
  main.py                              # FastAPI WebSocket entry point
  app/
    core/
      settings.py                      # Pydantic settings (.env)
      echo_config.py                   # ~/.echo/config.yaml loader
    lib/
      avatar/
        motion_controller.py           # Backend avatar motion runtime entry
        pose_manager.py                # Backend pose state machine
        expression_manager.py          # Backend expression state machine
        motion_types.py                # Motion frame schema
    services/
      local_audio_service.py           # Local microphone / speaker I/O via PyAudio
      vad_service.py                   # Silero VAD (512-sample chunks)
      stt_service.py                   # STT router
      llm_service.py                   # LLM router
      tts_service.py                   # TTS router
      brain_service.py                 # Conversation state + LLM coordinator
      kokoro_tts_service.py            # Local Kokoro TTS
      cartesia_tts_service.py          # Cloud Cartesia TTS
      mlx_qwen3_stt_service.py         # Local Qwen3 ASR
      mlx_whisper_stt_service.py       # Local Whisper ASR
      deepgram_stt_service.py          # Cloud Deepgram STT
      mlx_qwen3_llm_service.py         # Local Qwen3 LLM
      openai_llm_service.py            # Cloud OpenAI LLM
  ui/
    src/
      App.tsx                          # Main React UI, WebSocket control + state display
      App.css                          # Layout styles
      avatar/
        scene-manager.ts               # Three.js scene / camera / render loop
        avatar-manager.ts              # VRM load + motion application
        expression-manager.ts          # Apply backend expression channels
        pose-manager.ts                # Apply backend pose channels
        motion-types.ts                # Frontend motion frame types
        caption-manager.ts             # Subtitle timing driven by backend events
    public/
      vrm/Yuna.vrm                     # VRM avatar model
```

## Runtime Model

Echo now follows a **backend-owned audio pipeline**:

- Backend owns:
  - microphone capture
  - speaker playback
  - VAD / STT / LLM / TTS
  - avatar motion generation
- Frontend owns:
  - VRM rendering
  - chat / subtitle / status UI
  - start / stop interaction controls

The frontend no longer captures microphone PCM and no longer plays TTS audio directly.

## Development Commands

### Backend

```bash
# from project root
cp .env.example .env

# recommended: use project venv / uv
uv run python -m uvicorn main:app
```

Backend listens on `http://127.0.0.1:8000`.

### Frontend

```bash
cd ui
npm install
npm run dev
npm run build
npm run lint
```

Frontend dev server runs on `http://localhost:5173`.

## WebSocket Event Protocol

All frontend/backend communication goes through `ws://localhost:8000/ws`.

### Frontend → Backend

| Event | Payload | Description |
|------|------|-------------|
| `start_listen` | `{"event":"start_listen"}` | Start local microphone capture and conversation |
| `stop_listen` | `{"event":"stop_listen"}` | Stop local microphone capture and stop current speech |

No binary PCM is sent from the browser anymore.

### Backend → Frontend

| Event | Data | Description |
|------|------|-------------|
| `listening_state` | `{is_listening: boolean}` | Current conversation capture state |
| `vad_status` | `{is_speech: boolean}` | Current speech activity state |
| `chat_update` | `{role, content}` | New user / assistant message |
| `chat_status` | `{status: "idle" | "thinking"}` | LLM inference state |
| `tts_start` | `{}` | Backend speaker playback started |
| `tts_end` | `{}` | Backend speaker playback finished |
| `avatar_motion` | `AvatarMotionFrame` | Backend-generated avatar pose / expression frame |

## Audio Pipeline

### Input

- `LocalAudioService` opens the local microphone through `PyAudio`
- audio format is fixed at:
  - `16kHz`
  - `mono`
  - `Int16 PCM`
- captured PCM is pushed into an `asyncio.Queue`
- `main.py` drains the queue and feeds:
  - VAD
  - STT

### VAD

- `VADService.BUFFER_SIZE = 512`
- audio is accumulated in a per-session `audio_buffer`
- backend slices buffered audio into exact 512-sample chunks before VAD inference

### STT

- routed by `STTService`
- supported engines:
  - `mlx_qwen3`
  - `mlx_whisper`
  - `deepgram`

### LLM

- routed by `LLMService`
- supported engines:
  - `openai`
  - `mlx_qwen3`
- `BrainService` owns the rolling conversation history

### TTS + Playback

- routed by `TTSService`
- supported engines:
  - `kokoro`
  - `cartesia`
- backend directly plays generated PCM through `LocalAudioService.play_audio()`
- frontend only gets `tts_start` / `tts_end` for subtitle timing

## Avatar Motion

Backend motion is generated under `app/lib/avatar/`:

- `motion_controller.py`
- `pose_manager.py`
- `expression_manager.py`
- `motion_types.py`

The backend continuously pushes motion frames to the frontend.

Frontend avatar code only **applies** motion:

- `avatar-manager.ts`
- `pose-manager.ts`
- `expression-manager.ts`

There is no browser-side motion state machine for breathing / blinking / lip sync anymore.

## Current Constraints

- local audio I/O currently assumes **one active local capture session at a time**
- VAD chunk size must remain **512**
- local playback / local capture are handled in Python, not browser APIs
- TTS PCM is expected to be **16kHz Int16 mono**
- `BrainService` trims history to avoid unbounded context growth

## Engine Selection

Primary engine choices are read from `~/.echo/config.yaml` via `app/core/echo_config.py`.

Supported values:

```yaml
stt:
  engine: mlx_qwen3   # mlx_qwen3 | mlx_whisper | deepgram

llm:
  engine: openai      # openai | mlx_qwen3

tts:
  engine: kokoro      # kokoro | cartesia
```

## Environment Variables

Defined in `.env`:

```text
OPENAI_API_KEY
DEEPGRAM_API_KEY
CARTESIA_API_KEY
RUN_ENV
DEBUG_ENABLED
```

## Notes For Future Desktop Packaging

This repository is now structurally closer to a desktop-assistant architecture:

- backend already owns local audio devices
- frontend already behaves like a control / rendering surface
- the next natural step is wrapping the frontend in a desktop shell while keeping FastAPI as the local orchestration backend

## Docs

- Architecture overview: `AGENTS.md`
- VRM design docs: `docs/superpowers/specs/2026-03-31-vrm-avatar-design.md`
- VRM implementation plan: `docs/superpowers/plans/2026-03-31-vrm-avatar.md`
