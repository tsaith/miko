from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path

import yaml


@dataclass(slots=True)
class DeepgramConfig:
    api_key: str = ""
    model: str = "nova-2"
    language: str = "zh-TW"
    smart_format: bool = True


@dataclass(slots=True)
class OpenAIConfig:
    api_key: str = ""
    model: str = "gpt-4o-mini"
    max_output_tokens: int = 250
    temperature: float = 0.7


@dataclass(slots=True)
class CartesiaConfig:
    api_key: str = ""
    voice_id: str = "6eb8965c-e295-47bd-a9e4-3eeebb3abcff"
    model_id: str = "sonic-3"
    language: str = "zh"
    sample_rate: int = 16000
    api_version: str = "2025-04-16"


@dataclass(slots=True)
class MikoConfig:
    deepgram: DeepgramConfig
    openai: OpenAIConfig
    cartesia: CartesiaConfig


@dataclass(slots=True)
class BackendSettings:
    socket_path: Path | None = None
    config_path: Path | None = None
    workspace_dir: Path | None = None
    models_dir: Path | None = None

    def listen_target(self) -> str:
        if self.socket_path is None:
            raise RuntimeError("socket_path is required for unix transport")
        return f"unix://{self.socket_path}"

    def load_miko_config(self) -> MikoConfig:
        if self.config_path is None or not self.config_path.exists():
            return MikoConfig(
                deepgram=DeepgramConfig(),
                openai=OpenAIConfig(),
                cartesia=CartesiaConfig(),
            )

        raw = yaml.safe_load(self.config_path.read_text(encoding="utf-8")) or {}
        api_keys = raw.get("api_keys") or {}
        llm = raw.get("llm") or {}
        openai = llm.get("openai") or {}
        stt = raw.get("stt") or {}
        deepgram = stt.get("deepgram") or {}
        tts = raw.get("tts") or {}
        cartesia = tts.get("cartesia") or {}
        return MikoConfig(
            deepgram=DeepgramConfig(
                api_key=str(api_keys.get("deepgram") or "").strip(),
                model=str(deepgram.get("model") or "nova-2").strip() or "nova-2",
                language=str(deepgram.get("language") or "zh-TW").strip() or "zh-TW",
                smart_format=bool(deepgram.get("smart_format", True)),
            ),
            openai=OpenAIConfig(
                api_key=str(api_keys.get("openai") or "").strip(),
                model=str(openai.get("model") or "gpt-4o-mini").strip() or "gpt-4o-mini",
                max_output_tokens=int(openai.get("max_output_tokens") or 250),
                temperature=float(openai.get("temperature") or 0.7),
            ),
            cartesia=CartesiaConfig(
                api_key=str(api_keys.get("cartesia") or "").strip(),
                voice_id=str(cartesia.get("voice_id") or "6eb8965c-e295-47bd-a9e4-3eeebb3abcff").strip() or "6eb8965c-e295-47bd-a9e4-3eeebb3abcff",
                model_id=str(cartesia.get("model_id") or "sonic-3").strip() or "sonic-3",
                language=str(cartesia.get("language") or "zh").strip() or "zh",
                sample_rate=int(cartesia.get("sample_rate") or 16000),
                api_version=str(cartesia.get("api_version") or "2025-04-16").strip() or "2025-04-16",
            ),
        )


def _default_home_paths() -> tuple[Path, Path]:
    home = Path.home()
    base_dir = home / ".miko"
    return base_dir / "config.yaml", base_dir / "workspace"


def load_settings() -> BackendSettings:
    default_config_path, default_workspace_dir = _default_home_paths()
    config_path = Path(os.environ["MIKO_CONFIG_PATH"]).expanduser() if os.environ.get("MIKO_CONFIG_PATH") else default_config_path
    workspace_dir = Path(os.environ["MIKO_WORKSPACE_DIR"]).expanduser() if os.environ.get("MIKO_WORKSPACE_DIR") else default_workspace_dir
    models_dir = Path(os.environ["MIKO_MODELS_DIR"]).expanduser() if os.environ.get("MIKO_MODELS_DIR") else None
    socket_path = Path(os.environ["MIKO_BACKEND_SOCKET"]).expanduser() if os.environ.get("MIKO_BACKEND_SOCKET") else None
    if socket_path is None:
        socket_path = workspace_dir / "backend" / "miko-backend.sock"

    return BackendSettings(
        socket_path=socket_path,
        config_path=config_path,
        workspace_dir=workspace_dir,
        models_dir=models_dir,
    )
