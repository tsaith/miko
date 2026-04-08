from __future__ import annotations

import logging
import queue
import threading
import time
from dataclasses import dataclass, field

from app.config import BackendSettings
from app.generated import assistant_pb2, assistant_pb2_grpc

from .avatar.motion_controller import MotionController
from .brain_service import BrainService
from .chat.conversation_monitor import ConversationMonitor
from .chat.turn_agent import TurnAgent
from .deepgram_stt_service import TranscriptResult
from .event_bus import EventBus
from .llm_service import LLMService
from .stt_service import STTService
from .tts_service import TTSService


@dataclass(slots=True)
class ConversationSession:
    stt_session: object
    brain: BrainService
    turn_agent: TurnAgent
    monitor: ConversationMonitor
    sample_rate: int = 16000
    channels: int = 1
    pending_tts_audio: bytes = b""
    pending_tts_sample_rate: int = 16000
    pending_tts_channels: int = 1
    lock: threading.Lock = field(default_factory=threading.Lock)


class ConversationService(assistant_pb2_grpc.ConversationServiceServicer):
    def __init__(self, settings: BackendSettings, event_bus: EventBus, motion: MotionController) -> None:
        self._settings = settings
        self._config = settings.load_miko_config()
        self._event_bus = event_bus
        self._motion = motion
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
                turn_agent=TurnAgent(self._config.openai, self._config.turn_agent),
                monitor=ConversationMonitor(request.session_id),
            ),
        )
        self._motion.set_conversation_active(True)
        self._motion.set_phase("listen")
        session = self._get_session(request.session_id)
        if session is not None:
            session.monitor.on_assistant_listening()
        self._logger.info("session started session_id=%s", request.session_id)
        return assistant_pb2.StartSessionResponse(session_id=request.session_id, status="started")

    def StopSession(self, request: assistant_pb2.StopSessionRequest, context) -> assistant_pb2.StopSessionResponse:
        del context
        session = self._pop_session(request.session_id)
        if session is not None:
            self._finalize_session(request.session_id, session)
        self._motion.set_conversation_active(False)
        self._motion.set_phase("idle")
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
                    turn_agent=TurnAgent(self._config.openai, self._config.turn_agent),
                    monitor=ConversationMonitor(session_id),
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

    def NotifyPlaybackState(self, request: assistant_pb2.PlaybackStateRequest, context) -> assistant_pb2.Ack:
        del context
        session_id = str(request.session_id or "").strip()
        state = str(request.state or "").strip().lower()
        if not session_id:
            return assistant_pb2.Ack(ok=False, message="session_id is required")

        session = self._get_session(session_id)
        if session is None:
            return assistant_pb2.Ack(ok=False, message="session not found")

        if state == "started":
            duration = 0.0
            if session.pending_tts_audio:
                bytes_per_second = max(1, session.pending_tts_sample_rate * max(1, session.pending_tts_channels) * 2)
                duration = len(session.pending_tts_audio) / float(bytes_per_second)
                self._motion.enqueue_speech_audio(
                    session.pending_tts_audio,
                    session.pending_tts_sample_rate,
                    session.pending_tts_channels,
                )
                self._motion.begin_speaking(duration)
            session.monitor.on_assistant_speaking(duration_seconds=duration if duration > 0 else None)
            self._logger.info(
                "playback state session_id=%s state=started pending_bytes=%d sample_rate=%d channels=%d duration=%.3f",
                session_id,
                len(session.pending_tts_audio),
                session.pending_tts_sample_rate,
                session.pending_tts_channels,
                duration,
            )
            return assistant_pb2.Ack(ok=True, message="playback started")

        if state == "ended":
            session.pending_tts_audio = b""
            session.pending_tts_sample_rate = 16000
            session.pending_tts_channels = 1
            session.monitor.on_assistant_speak_end()
            session.monitor.on_assistant_listening()
            self._motion.end_speaking(reason="playback_end")
            self._logger.info("playback state session_id=%s state=ended", session_id)
            return assistant_pb2.Ack(ok=True, message="playback ended")

        return assistant_pb2.Ack(ok=False, message=f"unsupported playback state: {state}")

    def _handle_transcript(self, session_id: str, session: ConversationSession, transcript: TranscriptResult) -> None:
        text = transcript.text.strip()
        if not text:
            return

        is_final = transcript.kind == "final"
        session.turn_agent.push_user_text(text, is_final=is_final)
        accumulated_user_text = session.turn_agent.current_user_text()
        session.monitor.on_user_speaking(accumulated_user_text)
        self._publish_transcript(session_id, accumulated_user_text if is_final else text, kind=transcript.kind)

        if not is_final:
            preview = session.turn_agent.preview_user_speak_end()
            self._logger.info(
                "transcript partial session_id=%s chars=%d accumulated_chars=%d preview_turn_end=%s source=%s",
                session_id,
                len(text),
                len(accumulated_user_text),
                preview.is_user_speak_end,
                preview.source,
            )
            return

        should_reply = session.turn_agent.is_user_speak_end()
        decision = session.turn_agent.last_decision()

        self._logger.info(
            "transcript final session_id=%s chars=%d accumulated_chars=%d turn_end=%s source=%s",
            session_id,
            len(text),
            len(accumulated_user_text),
            should_reply,
            decision.source if decision is not None else "-",
        )

        if not should_reply:
            return

        user_text = session.turn_agent.consume_user_text().strip()
        if not user_text:
            return

        session.monitor.on_user_speak_end(user_text)
        self._publish_chat(session_id, role="user", content=user_text)
        self._publish_chat(session_id, status="thinking")
        session.monitor.on_assistant_thinking(user_text)
        self._motion.set_phase("think")

        try:
            reply = session.brain.process_message(user_text, session_id=session_id)
        except Exception as exc:
            self._logger.warning("llm failed session_id=%s err=%s", session_id, exc)
            self._publish_chat(session_id, role="assistant", content="抱歉，我剛剛連不上雲端服務，請稍後再試。")
            self._publish_error(session_id, f"LLM 失敗：{exc}")
            session.turn_agent.push_assistant_text("抱歉，我剛剛連不上雲端服務，請稍後再試。")
            session.monitor.on_assistant_text("抱歉，我剛剛連不上雲端服務，請稍後再試。")
            session.monitor.on_assistant_listening()
            self._motion.set_phase("listen")
            self._publish_chat(session_id, status="idle")
            return

        session.turn_agent.push_assistant_text(reply)
        session.monitor.on_assistant_text(reply)
        self._publish_chat(session_id, role="assistant", content=reply)

        try:
            audio = self._tts.generate_audio(reply, session_id=session_id)
        except Exception as exc:
            self._logger.warning("tts failed session_id=%s err=%s", session_id, exc)
            self._publish_error(session_id, f"TTS 失敗：{exc}")
            session.monitor.on_assistant_listening()
            self._motion.set_phase("listen")
            self._publish_chat(session_id, status="idle")
            return

        if not audio:
            self._publish_error(session_id, "TTS 失敗：Cartesia 未回傳音訊")
            session.monitor.on_assistant_listening()
            self._motion.set_phase("listen")
            self._publish_chat(session_id, status="idle")
            return

        tts_sample_rate = self._tts.output_sample_rate()
        tts_channels = session.channels
        self._logger.info(
            "tts playback prepared session_id=%s bytes=%d sample_rate=%d channels=%d",
            session_id,
            len(audio),
            tts_sample_rate,
            tts_channels,
        )
        session.pending_tts_audio = audio
        session.pending_tts_sample_rate = tts_sample_rate
        session.pending_tts_channels = tts_channels
        self._event_bus.publish(
            assistant_pb2.BackendEvent(
                session_id=session_id,
                tts_lifecycle=assistant_pb2.TtsLifecycleEvent(state="start"),
            )
        )
        self._motion.enqueue_speech_audio(audio, tts_sample_rate, tts_channels)
        self._logger.info(
            "tts speech audio queued session_id=%s bytes=%d sample_rate=%d channels=%d",
            session_id,
            len(audio),
            tts_sample_rate,
            tts_channels,
        )
        self._event_bus.publish(
            assistant_pb2.BackendEvent(
                session_id=session_id,
                tts_chunk=assistant_pb2.TtsChunkEvent(
                    pcm_s16le=audio,
                    sample_rate=tts_sample_rate,
                    channels=tts_channels,
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

    def _publish_transcript(self, session_id: str, text: str, kind: str = "final") -> None:
        self._event_bus.publish(
            assistant_pb2.BackendEvent(
                session_id=session_id,
                transcript=assistant_pb2.TranscriptEvent(kind=kind, text=text),
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
