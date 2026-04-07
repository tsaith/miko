from __future__ import annotations

import logging
import queue
import threading
from dataclasses import dataclass, field

from app.config import BackendSettings
from app.generated import assistant_pb2, assistant_pb2_grpc

from .brain_service import BrainService
from .event_bus import EventBus
from .llm_service import LLMService
from .stt_service import STTService
from .tts_service import TTSService


@dataclass(slots=True)
class ConversationSession:
    stt_session: object
    brain: BrainService
    sample_rate: int = 16000
    channels: int = 1
    lock: threading.Lock = field(default_factory=threading.Lock)


class ConversationService(assistant_pb2_grpc.ConversationServiceServicer):
    def __init__(self, settings: BackendSettings, event_bus: EventBus) -> None:
        self._settings = settings
        self._config = settings.load_miko_config()
        self._event_bus = event_bus
        self._logger = logging.getLogger("miko.backend.conversation")
        self._stt = STTService(self._config.deepgram)
        self._llm = LLMService(self._config.openai)
        self._tts = TTSService(self._config.cartesia)
        self._sessions: dict[str, ConversationSession] = {}
        self._sessions_lock = threading.Lock()

    def StartSession(self, request: assistant_pb2.StartSessionRequest, context) -> assistant_pb2.StartSessionResponse:
        del context
        self._set_session(
            request.session_id,
            ConversationSession(
                stt_session=self._stt.create_live_session(),
                brain=BrainService(self._llm),
            ),
        )
        self._logger.info("session started session_id=%s", request.session_id)
        return assistant_pb2.StartSessionResponse(session_id=request.session_id, status="started")

    def StopSession(self, request: assistant_pb2.StopSessionRequest, context) -> assistant_pb2.StopSessionResponse:
        del context
        session = self._pop_session(request.session_id)
        if session is not None:
            self._finalize_session(request.session_id, session)
        self._logger.info("session stopped session_id=%s", request.session_id)
        return assistant_pb2.StopSessionResponse(session_id=request.session_id, status="stopped")

    def StreamAudio(self, request_iterator, context) -> assistant_pb2.Ack:
        del context
        for chunk in request_iterator:
            session_id = str(chunk.session_id or "").strip()
            if not session_id:
                continue

            session = self._get_session(session_id)
            if session is None:
                session = ConversationSession(
                    stt_session=self._stt.create_live_session(
                        sample_rate=chunk.sample_rate or 16000,
                        channels=chunk.channels or 1,
                    ),
                    brain=BrainService(self._llm),
                )
                self._set_session(session_id, session)

            with session.lock:
                transcripts = self._stt.push_audio(
                    session.stt_session,
                    bytes(chunk.pcm_s16le),
                    chunk.sample_rate or 16000,
                    chunk.channels or 1,
                )
                session.sample_rate = chunk.sample_rate or 16000
                session.channels = chunk.channels or 1

            for transcript in transcripts:
                self._handle_transcript(session_id, session, transcript)

        return assistant_pb2.Ack(ok=True, message="audio stream closed")

    def StreamEvents(self, request: assistant_pb2.StreamEventsRequest, context):
        subscriber = self._event_bus.subscribe()
        try:
            while context.is_active():
                try:
                    event = subscriber.get(timeout=0.5)
                except queue.Empty:
                    continue

                if request.session_id and event.session_id and event.session_id != request.session_id:
                    continue
                yield event
        finally:
            self._event_bus.unsubscribe(subscriber)
            self._logger.info("event stream closed session_id=%s", request.session_id or "<global>")

    def _handle_transcript(self, session_id: str, session: ConversationSession, transcript: str) -> None:
        text = transcript.strip()
        if not text:
            return

        self._logger.info("transcript final session_id=%s chars=%d", session_id, len(text))
        self._publish_transcript(session_id, text)
        self._publish_chat(session_id, role="user", content=text)
        self._publish_chat(session_id, status="thinking")

        try:
            reply = session.brain.process_message(text, session_id=session_id)
        except Exception as exc:
            self._logger.warning("llm failed session_id=%s err=%s", session_id, exc)
            self._publish_chat(session_id, role="assistant", content="抱歉，我剛剛連不上雲端服務，請稍後再試。")
            self._publish_error(session_id, f"LLM 失敗：{exc}")
            self._publish_chat(session_id, status="idle")
            return

        self._publish_chat(session_id, role="assistant", content=reply)

        try:
            audio = self._tts.generate_audio(reply, session_id=session_id)
        except Exception as exc:
            self._logger.warning("tts failed session_id=%s err=%s", session_id, exc)
            self._publish_error(session_id, f"TTS 失敗：{exc}")
            self._publish_chat(session_id, status="idle")
            return

        if not audio:
            self._publish_error(session_id, "TTS 失敗：Cartesia 未回傳音訊")
            self._publish_chat(session_id, status="idle")
            return

        self._event_bus.publish(
            assistant_pb2.BackendEvent(
                session_id=session_id,
                tts_lifecycle=assistant_pb2.TtsLifecycleEvent(state="start"),
            )
        )
        self._event_bus.publish(
            assistant_pb2.BackendEvent(
                session_id=session_id,
                tts_chunk=assistant_pb2.TtsChunkEvent(
                    pcm_s16le=audio,
                    sample_rate=session.sample_rate,
                    channels=session.channels,
                ),
            )
        )
        self._event_bus.publish(
            assistant_pb2.BackendEvent(
                session_id=session_id,
                tts_lifecycle=assistant_pb2.TtsLifecycleEvent(state="end"),
            )
        )
        self._publish_chat(session_id, status="idle")

    def _finalize_session(self, session_id: str, session: ConversationSession) -> None:
        with session.lock:
            transcripts = self._stt.finish_live(session.stt_session)
        for transcript in transcripts:
            self._handle_transcript(session_id, session, transcript)

    def _get_session(self, session_id: str) -> ConversationSession | None:
        with self._sessions_lock:
            return self._sessions.get(session_id)

    def _set_session(self, session_id: str, session: ConversationSession) -> None:
        with self._sessions_lock:
            self._sessions[session_id] = session

    def _pop_session(self, session_id: str) -> ConversationSession | None:
        with self._sessions_lock:
            return self._sessions.pop(session_id, None)

    def _publish_transcript(self, session_id: str, text: str) -> None:
        self._event_bus.publish(
            assistant_pb2.BackendEvent(
                session_id=session_id,
                transcript=assistant_pb2.TranscriptEvent(kind="final", text=text),
            )
        )

    def _publish_chat(self, session_id: str, role: str = "", content: str = "", status: str = "") -> None:
        self._event_bus.publish(
            assistant_pb2.BackendEvent(
                session_id=session_id,
                chat=assistant_pb2.ChatEvent(role=role, content=content, status=status),
            )
        )

    def _publish_error(self, session_id: str, message: str) -> None:
        self._event_bus.publish(
            assistant_pb2.BackendEvent(
                session_id=session_id,
                error=assistant_pb2.ErrorEvent(message=message),
            )
        )
