from __future__ import annotations

import io
import json
import logging
import time
import urllib.error
import urllib.parse
import urllib.request
import wave
from dataclasses import dataclass, field

from app.config import DeepgramConfig
from .vad_service import VADService


@dataclass(slots=True)
class DeepgramLiveSession:
    sample_rate: int = 16000
    channels: int = 1
    buffer: bytearray = field(default_factory=bytearray)
    vad_buffer: bytearray = field(default_factory=bytearray)
    speaking: bool = False
    silence_chunks: int = 0


class DeepgramSTTService:
    """
    Cloud STT service using Deepgram.
    The service keeps the same responsibility boundary as the reference project:
    it accepts live PCM audio and produces finalized utterances.
    """

    def __init__(self, config: DeepgramConfig, vad: VADService) -> None:
        self.api_key = config.api_key
        self.model = config.model
        self.language = config.language
        self.smart_format = config.smart_format
        self.vad = vad
        self.silence_chunks_to_finalize = 14
        self.min_utterance_bytes = 3200
        self._logger = logging.getLogger("miko.backend.stt.deepgram")

    def create_live_session(self, sample_rate: int = 16000, channels: int = 1) -> DeepgramLiveSession:
        return DeepgramLiveSession(sample_rate=sample_rate, channels=channels)

    def push_audio(self, session: DeepgramLiveSession, pcm_s16le: bytes, sample_rate: int, channels: int) -> list[str]:
        if not pcm_s16le:
            return []

        session.sample_rate = sample_rate
        session.channels = channels
        session.vad_buffer.extend(pcm_s16le)

        if self._contains_speech(session):
            session.buffer.extend(pcm_s16le)
            session.speaking = True
            session.silence_chunks = 0
            return []

        if not session.speaking:
            return []

        session.buffer.extend(pcm_s16le)
        session.silence_chunks += 1
        if session.silence_chunks < self.silence_chunks_to_finalize:
            return []
        return self.finish_live(session)

    def finish_live(self, session: DeepgramLiveSession) -> list[str]:
        if len(session.buffer) < self.min_utterance_bytes:
            self._logger.debug("discard short utterance bytes=%d", len(session.buffer))
            self._reset_session(session)
            return []

        pcm_s16le = bytes(session.buffer)
        sample_rate = session.sample_rate
        channels = session.channels
        self._reset_session(session)

        transcript = self._transcribe_once(pcm_s16le, sample_rate, channels)
        if not transcript:
            return []
        return [transcript]

    def _reset_session(self, session: DeepgramLiveSession) -> None:
        session.buffer.clear()
        session.vad_buffer.clear()
        session.speaking = False
        session.silence_chunks = 0

    def _contains_speech(self, session: DeepgramLiveSession) -> bool:
        frame_bytes = self.vad.frame_bytes(session.sample_rate, session.channels)
        detected = False
        while len(session.vad_buffer) >= frame_bytes:
            chunk = bytes(session.vad_buffer[:frame_bytes])
            del session.vad_buffer[:frame_bytes]
            if self.vad.is_speech(chunk, session.sample_rate, session.channels):
                detected = True
        return detected

    def _transcribe_once(self, pcm_s16le: bytes, sample_rate: int, channels: int) -> str:
        if not self.api_key.strip():
            raise RuntimeError("deepgram api key is missing")
        started = time.perf_counter()
        self._logger.info(
            "request start model=%s language=%s bytes=%d sample_rate=%d channels=%d",
            self.model,
            self.language,
            len(pcm_s16le),
            sample_rate,
            channels,
        )

        query = urllib.parse.urlencode(
            {
                "model": self.model,
                "language": self.language,
                "smart_format": "true" if self.smart_format else "false",
                "punctuate": "true",
            }
        )
        request = urllib.request.Request(
            url=f"https://api.deepgram.com/v1/listen?{query}",
            data=self._pcm_to_wav(pcm_s16le, sample_rate, channels),
            method="POST",
            headers={
                "Authorization": f"Token {self.api_key}",
                "Content-Type": "audio/wav",
                "Accept": "application/json",
            },
        )

        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                payload = json.loads(response.read().decode("utf-8"))
        except urllib.error.HTTPError as exc:
            body = exc.read().decode("utf-8", errors="ignore")
            self._logger.warning(
                "request failed status=%s after=%sms body=%s",
                exc.code,
                round((time.perf_counter() - started) * 1000),
                body.strip(),
            )
            raise RuntimeError(f"deepgram http {exc.code}: {body}") from exc
        except urllib.error.URLError as exc:
            self._logger.warning(
                "request failed after=%sms reason=%s",
                round((time.perf_counter() - started) * 1000),
                exc.reason,
            )
            raise RuntimeError(f"deepgram request failed: {exc.reason}") from exc

        channel = ((payload.get("results") or {}).get("channels") or [{}])[0]
        alternative = (channel.get("alternatives") or [{}])[0]
        transcript = str(alternative.get("transcript") or "").strip()
        self._logger.info(
            "request done after=%sms chars=%d",
            round((time.perf_counter() - started) * 1000),
            len(transcript),
        )
        return transcript

    def _pcm_to_wav(self, pcm_s16le: bytes, sample_rate: int, channels: int) -> bytes:
        buffer = io.BytesIO()
        with wave.open(buffer, "wb") as handle:
            handle.setnchannels(channels)
            handle.setsampwidth(2)
            handle.setframerate(sample_rate)
            handle.writeframes(pcm_s16le)
        return buffer.getvalue()
