from __future__ import annotations

from app.config import DeepgramConfig

from .deepgram_stt_service import DeepgramLiveSession, DeepgramSTTService
from .vad_service import VADService


class STTService:
    """
    STT router for the Python sidecar.
    The current project only supports Deepgram, but keeps a router boundary
    so the pipeline shape matches the reference architecture.
    """

    def __init__(self, config: DeepgramConfig, engine: str = "deepgram") -> None:
        self.engine = engine
        self._vad = VADService()
        self._deepgram = DeepgramSTTService(config, self._vad)

    def _get_engine(self) -> DeepgramSTTService:
        if self.engine != "deepgram":
            raise ValueError(f"unknown stt engine: {self.engine}")
        return self._deepgram

    def create_live_session(self, sample_rate: int = 16000, channels: int = 1) -> DeepgramLiveSession:
        return self._get_engine().create_live_session(sample_rate=sample_rate, channels=channels)

    def push_audio(self, session: DeepgramLiveSession, pcm_s16le: bytes, sample_rate: int, channels: int) -> list[str]:
        return self._get_engine().push_audio(session, pcm_s16le, sample_rate, channels)

    def finish_live(self, session: DeepgramLiveSession) -> list[str]:
        return self._get_engine().finish_live(session)
