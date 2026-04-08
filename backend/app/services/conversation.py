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
    barge_in_until: float = 0.0
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
            session.monitor.on_assistant_speaking()
            self._logger.info("playback state session_id=%s state=started", session_id)
            return assistant_pb2.Ack(ok=True, message="playback started")

        if state in {"ended", "interrupted"}:
            if state == "interrupted":
                session.barge_in_until = time.monotonic() + 1.5
                session.monitor.on_assistant_speak_end("playback_interrupted")
                session.monitor.on_assistant_listening("assistant_listening_after_interrupt")
            else:
                session.barge_in_until = 0.0
                session.monitor.on_assistant_speak_end()
                session.monitor.on_assistant_listening()
            self._motion.end_speaking(reason=f"playback_{state}")
            self._logger.info("playback state session_id=%s state=%s", session_id, state)
            return assistant_pb2.Ack(ok=True, message=f"playback {state}")

        return assistant_pb2.Ack(ok=False, message=f"unsupported playback state: {state}")

    def ConfirmBargeIn(
        self,
        request: assistant_pb2.BargeInConfirmRequest,
        context,
    ) -> assistant_pb2.BargeInConfirmResponse:
        del context
        pcm_s16le = bytes(request.pcm_s16le)
        if not pcm_s16le:
            return assistant_pb2.BargeInConfirmResponse(confirmed=False, speech_ms=0, speech_frames=0)

        sample_rate = int(request.sample_rate or 16000)
        channels = int(request.channels or 1)
        min_speech_ms = int(request.min_speech_ms or self._config.barge_in.confirm_speech_ms)
        confirmed, speech_ms, speech_frames = self._stt.confirm_barge_in(
            pcm_s16le,
            sample_rate,
            channels,
            min_speech_ms,
        )
        self._logger.info(
            "barge-in confirm session_id=%s confirmed=%s speech_ms=%d frames=%d",
            str(request.session_id or "").strip() or "-",
            confirmed,
            speech_ms,
            speech_frames,
        )
        return assistant_pb2.BargeInConfirmResponse(
            confirmed=confirmed,
            speech_ms=speech_ms,
            speech_frames=speech_frames,
        )

    def _handle_transcript(self, session_id: str, session: ConversationSession, transcript: TranscriptResult) -> None:
        text = transcript.text.strip()
        if not text:
            return

        is_final = transcript.kind == "final"
        is_barge_in = session.barge_in_until > 0 and time.monotonic() <= session.barge_in_until
        session.turn_agent.push_user_text(text, is_final=is_final)
        accumulated_user_text = session.turn_agent.current_user_text()
        if is_barge_in:
            session.monitor.on_user_speaking(accumulated_user_text, "user_speaking_barge_in")
            self._logger.info(
                "barge-in transcript session_id=%s kind=%s chars=%d accumulated_chars=%d",
                session_id,
                transcript.kind,
                len(text),
                len(accumulated_user_text),
            )
        else:
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

        if is_barge_in:
            session.barge_in_until = 0.0
            session.monitor.on_user_speak_end(user_text, "user_speak_end_barge_in")
        else:
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
        bytes_per_second = max(1, tts_sample_rate*max(1, tts_channels)*2)
        playback_duration = len(audio) / float(bytes_per_second)
        self._logger.info(
            "tts playback prepared session_id=%s bytes=%d sample_rate=%d channels=%d duration=%.3fs",
            session_id,
            len(audio),
            tts_sample_rate,
            tts_channels,
            playback_duration,
        )
        session.monitor.on_assistant_speaking(reply, playback_duration)
        self._motion.begin_speaking(playback_duration)
        self._event_bus.publish(
            assistant_pb2.BackendEvent(
                session_id=session_id,
                tts_lifecycle=assistant_pb2.TtsLifecycleEvent(state="start"),
            )
        )
        self._motion.enqueue_speech_audio(audio, tts_sample_rate, tts_channels)
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
