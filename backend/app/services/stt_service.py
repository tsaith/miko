from __future__ import annotations

from app.config import DeepgramConfig

from .deepgram_stt_service import DeepgramLiveSession, DeepgramSTTService, TranscriptResult
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

    def push_audio(self, session: DeepgramLiveSession, pcm_s16le: bytes, sample_rate: int, channels: int) -> list[TranscriptResult]:
        return self._get_engine().push_audio(session, pcm_s16le, sample_rate, channels)

    def finish_live(self, session: DeepgramLiveSession) -> list[TranscriptResult]:
        return self._get_engine().finish_live(session)

    def confirm_barge_in(
        self,
        pcm_s16le: bytes,
        sample_rate: int,
        channels: int,
        min_speech_ms: int,
    ) -> tuple[bool, int, int]:
        timestamps = self._vad.get_timestamps(pcm_s16le, sample_rate, channels)
        speech_samples = 0
        for item in timestamps:
            start = int(item.get("start") or 0)
            end = int(item.get("end") or 0)
            if end > start:
                speech_samples += end - start
        speech_ms = round((speech_samples / max(1, sample_rate)) * 1000)
        confirmed = speech_ms >= max(1, min_speech_ms)
        return confirmed, speech_ms, len(timestamps)
