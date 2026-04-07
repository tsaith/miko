from __future__ import annotations

from app.config import CartesiaConfig

from .cartesia_tts_service import CartesiaTTSService


class TTSService:
    """
    Router for text-to-speech requests.
    The current project supports Cartesia only.
    """

    def __init__(self, config: CartesiaConfig, engine: str = "cartesia") -> None:
        self.engine = engine
        self._cartesia = CartesiaTTSService(config)

    def _get_engine(self) -> CartesiaTTSService:
        if self.engine != "cartesia":
            raise ValueError(f"unknown tts engine: {self.engine}")
        return self._cartesia

    def generate_audio(self, text: str, session_id: str = "") -> bytes:
        return self._get_engine().generate_audio(text, session_id=session_id)

    def output_sample_rate(self) -> int:
        return self._get_engine().output_sample_rate()
