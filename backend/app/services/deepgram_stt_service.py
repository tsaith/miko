from __future__ import annotations

import io
import json
import logging
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import wave
from dataclasses import dataclass, field

from app.config import DeepgramConfig
from .vad_service import VADService


@dataclass(slots=True)
class TranscriptResult:
    kind: str
    text: str


@dataclass(slots=True)
class DeepgramLiveSession:
    sample_rate: int = 16000
    channels: int = 1
    buffer: bytearray = field(default_factory=bytearray)
    vad_buffer: bytearray = field(default_factory=bytearray)
    speaking: bool = False
    silence_chunks: int = 0
    speech_bytes: int = 0
    silence_bytes: int = 0
    last_partial_text: str = ""
    last_partial_at: float = 0.0
    pending_partials: list[TranscriptResult] = field(default_factory=list)
    partial_generation: int = 0
    partial_in_flight_generation: int | None = None
    lock: threading.Lock = field(default_factory=threading.Lock)


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
        self.endpointing_ms = max(200, config.endpointing_ms)
        self.utterance_end_ms = max(self.endpointing_ms, config.utterance_end_ms)
        self.vad = vad
        self.min_utterance_bytes = 3200
        self.partial_min_bytes = 6400
        self.partial_interval_seconds = 0.9
        self.min_utterance_ms = 850
        self.force_finalize_ms = max(self.utterance_end_ms+900, 1800)
        self.long_utterance_ms = 2200
        self._logger = logging.getLogger("miko.backend.stt.deepgram")

    def create_live_session(self, sample_rate: int = 16000, channels: int = 1) -> DeepgramLiveSession:
        self.vad.reset_stream_state()
        return DeepgramLiveSession(sample_rate=sample_rate, channels=channels)

    def push_audio(self, session: DeepgramLiveSession, pcm_s16le: bytes, sample_rate: int, channels: int) -> list[TranscriptResult]:
        results = self._drain_partial_results(session)
        if not pcm_s16le:
            return results

        session.sample_rate = sample_rate
        session.channels = channels
        session.vad_buffer.extend(pcm_s16le)

        if self._contains_speech(session):
            session.buffer.extend(pcm_s16le)
            session.speaking = True
            session.silence_chunks = 0
            session.speech_bytes += len(pcm_s16le)
            session.silence_bytes = 0
            self._maybe_schedule_partial(session)
            return results

        if not session.speaking:
            return results

        session.buffer.extend(pcm_s16le)
        session.silence_chunks += 1
        session.silence_bytes += len(pcm_s16le)
        if not self._should_finalize(session):
            self._maybe_schedule_partial(session)
            return results
        return results + self.finish_live(session)

    def finish_live(self, session: DeepgramLiveSession) -> list[TranscriptResult]:
        results = self._drain_partial_results(session)
        pcm_s16le = self._buffer_without_trailing_silence(session)
        if len(pcm_s16le) < self.min_utterance_bytes:
            self._logger.debug("discard short utterance bytes=%d", len(pcm_s16le))
            self._reset_session(session)
            return results

        sample_rate = session.sample_rate
        channels = session.channels
        self._reset_session(session)

        transcript = self._transcribe_once(pcm_s16le, sample_rate, channels)
        if not transcript:
            return results
        return results + [TranscriptResult(kind="final", text=transcript)]

    def _reset_session(self, session: DeepgramLiveSession) -> None:
        session.buffer.clear()
        session.vad_buffer.clear()
        session.speaking = False
        session.silence_chunks = 0
        session.speech_bytes = 0
        session.silence_bytes = 0
        session.last_partial_text = ""
        session.last_partial_at = 0.0
        with session.lock:
            session.pending_partials.clear()
            session.partial_generation += 1
            if session.partial_in_flight_generation == session.partial_generation-1:
                session.partial_in_flight_generation = None
        self.vad.reset_stream_state()

    def _maybe_schedule_partial(self, session: DeepgramLiveSession) -> None:
        payload = self._buffer_without_trailing_silence(session)
        if len(payload) < self.partial_min_bytes:
            return
        now = time.monotonic()
        if session.last_partial_at > 0 and (now - session.last_partial_at) < self.partial_interval_seconds:
            return

        with session.lock:
            generation = session.partial_generation
            if session.partial_in_flight_generation == generation:
                return
            session.partial_in_flight_generation = generation
            session.last_partial_at = now

        worker = threading.Thread(
            target=self._run_partial_request,
            args=(session, generation, payload, session.sample_rate, session.channels),
            name="miko-stt-partial",
            daemon=True,
        )
        worker.start()

    def _should_finalize(self, session: DeepgramLiveSession) -> bool:
        silence_ms = self._bytes_to_ms(session.silence_bytes, session.sample_rate, session.channels)
        if silence_ms < self.endpointing_ms:
            return False

        speech_ms = self._bytes_to_ms(session.speech_bytes, session.sample_rate, session.channels)
        transcript_tail = session.last_partial_text.strip()
        if speech_ms < self.min_utterance_ms and silence_ms < self.utterance_end_ms:
            return False
        if self._looks_complete(transcript_tail):
            return True
        if not self._looks_unfinished(transcript_tail) and silence_ms >= self.utterance_end_ms and speech_ms >= self.min_utterance_ms:
            return True
        if self._looks_unfinished(transcript_tail) and silence_ms < self.force_finalize_ms:
            return False
        if speech_ms >= self.long_utterance_ms and silence_ms >= max(self.endpointing_ms, 700):
            return True
        return silence_ms >= self.force_finalize_ms

    def _buffer_without_trailing_silence(self, session: DeepgramLiveSession) -> bytes:
        if session.silence_bytes <= 0 or session.silence_bytes >= len(session.buffer):
            return bytes(session.buffer)
        return bytes(session.buffer[:-session.silence_bytes])

    def _bytes_to_ms(self, total_bytes: int, sample_rate: int, channels: int) -> int:
        bytes_per_second = max(1, sample_rate*max(1, channels)*2)
        return round((total_bytes / bytes_per_second) * 1000)

    def _looks_complete(self, transcript: str) -> bool:
        text = transcript.strip()
        if not text:
            return False
        if text.endswith(("。", "！", "？", ".", "!", "?")):
            return True
        return text.endswith(("嗎", "呢", "吧", "是不是", "對嗎", "好嗎"))

    def _looks_unfinished(self, transcript: str) -> bool:
        text = transcript.strip()
        if not text:
            return False
        if text.endswith(("，", "、", ",", "...", "…")):
            return True
        return text.endswith(("然後", "所以", "因為", "如果", "但是", "而且", "就是", "還有", "例如", "比如"))

    def _contains_speech(self, session: DeepgramLiveSession) -> bool:
        frame_bytes = self.vad.frame_bytes(session.sample_rate, session.channels)
        detected = False
        while len(session.vad_buffer) >= frame_bytes:
            chunk = bytes(session.vad_buffer[:frame_bytes])
            del session.vad_buffer[:frame_bytes]
            if self.vad.is_speech(chunk, session.sample_rate, session.channels):
                detected = True
        return detected

    def _drain_partial_results(self, session: DeepgramLiveSession) -> list[TranscriptResult]:
        with session.lock:
            if not session.pending_partials:
                return []
            results = list(session.pending_partials)
            session.pending_partials.clear()
            return results

    def _run_partial_request(
        self,
        session: DeepgramLiveSession,
        generation: int,
        payload: bytes,
        sample_rate: int,
        channels: int,
    ) -> None:
        try:
            transcript = self._transcribe_once(payload, sample_rate, channels).strip()
        except Exception as exc:
            self._logger.warning("partial request failed after schedule err=%s", exc)
            transcript = ""

        with session.lock:
            if session.partial_generation != generation:
                return
            if session.partial_in_flight_generation == generation:
                session.partial_in_flight_generation = None
            if not transcript or transcript == session.last_partial_text:
                return
            session.last_partial_text = transcript
            session.pending_partials.append(TranscriptResult(kind="partial", text=transcript))

        self._logger.info("partial transcript chars=%d", len(transcript))

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
