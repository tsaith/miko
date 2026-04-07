from __future__ import annotations

import json
import logging
import time
import urllib.error
import urllib.request

from app.config import CartesiaConfig


class CartesiaTTSService:
    """
    Text-to-speech service using Cartesia cloud API.
    """

    def __init__(self, config: CartesiaConfig) -> None:
        self.api_key = config.api_key
        self.voice_id = config.voice_id
        self.model_id = config.model_id
        self.language = config.language
        self.sample_rate = config.sample_rate
        self.api_version = config.api_version
        self._logger = logging.getLogger("miko.backend.tts.cartesia")

    def generate_audio(self, text: str, session_id: str = "") -> bytes:
        if not self.api_key.strip():
            raise RuntimeError("cartesia api key is missing")
        started = time.perf_counter()
        self._logger.info(
            "request start session_id=%s model=%s voice=%s chars=%d",
            session_id or "-",
            self.model_id,
            self.voice_id,
            len(text),
        )

        payload = json.dumps(
            {
                "model_id": self.model_id,
                "transcript": text,
                "voice": {"mode": "id", "id": self.voice_id},
                "language": self.language,
                "output_format": {
                    "container": "raw",
                    "encoding": "pcm_s16le",
                    "sample_rate": self.sample_rate,
                },
            }
        ).encode("utf-8")

        request = urllib.request.Request(
            url="https://api.cartesia.ai/tts/bytes",
            data=payload,
            method="POST",
            headers={
                "Content-Type": "application/json",
                "Cartesia-Version": self.api_version,
                "X-API-Key": self.api_key,
                "Authorization": f"Bearer {self.api_key}",
            },
        )

        try:
            with urllib.request.urlopen(request, timeout=120) as response:
                audio = response.read()
        except urllib.error.HTTPError as exc:
            body = exc.read().decode("utf-8", errors="ignore")
            self._logger.warning(
                "request failed session_id=%s status=%s after=%sms body=%s",
                session_id or "-",
                exc.code,
                round((time.perf_counter() - started) * 1000),
                body.strip(),
            )
            raise RuntimeError(f"cartesia http {exc.code}: {body}") from exc
        except urllib.error.URLError as exc:
            self._logger.warning(
                "request failed session_id=%s after=%sms reason=%s",
                session_id or "-",
                round((time.perf_counter() - started) * 1000),
                exc.reason,
            )
            raise RuntimeError(f"cartesia request failed: {exc.reason}") from exc
        self._logger.info(
            "request done session_id=%s after=%sms bytes=%d sample_rate=%d",
            session_id or "-",
            round((time.perf_counter() - started) * 1000),
            len(audio),
            self.sample_rate,
        )
        return audio
