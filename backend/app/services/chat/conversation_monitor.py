from __future__ import annotations

from dataclasses import dataclass, field
from enum import StrEnum
import logging
import threading
import time


class ConversationState(StrEnum):
    ASSISTANT_LISTENING = "assistant_listening"
    ASSISTANT_THINKING = "assistant_thinking"
    ASSISTANT_SPEAKING = "assistant_speaking"
    ASSISTANT_SPEAK_END = "assistant_speak_end"
    USER_SPEAKING = "user_speaking"
    USER_SPEAK_END = "user_speak_end"


@dataclass(slots=True)
class ConversationSnapshot:
    state: str
    last_user_text: str = ""
    last_assistant_text: str = ""
    updated_at: float = field(default_factory=time.monotonic)


class ConversationMonitor:
    def __init__(self, session_id: str = "") -> None:
        self._session_id = session_id or "-"
        self._logger = logging.getLogger("miko.backend.chat.conversation_monitor")
        self._lock = threading.Lock()
        self._state = ConversationState.ASSISTANT_LISTENING
        self._last_user_text = ""
        self._last_assistant_text = ""
        self._updated_at = time.monotonic()
        self._speak_timer: threading.Timer | None = None

    def reset(self) -> None:
        with self._lock:
            self._cancel_speak_timer()
            self._state = ConversationState.ASSISTANT_LISTENING
            self._last_user_text = ""
            self._last_assistant_text = ""
            self._updated_at = time.monotonic()

    def state(self) -> str:
        with self._lock:
            return self._state.value

    def snapshot(self) -> ConversationSnapshot:
        with self._lock:
            return ConversationSnapshot(
                state=self._state.value,
                last_user_text=self._last_user_text,
                last_assistant_text=self._last_assistant_text,
                updated_at=self._updated_at,
            )

    def on_assistant_listening(self, reason: str = "assistant_listening") -> None:
        with self._lock:
            self._cancel_speak_timer()
            self._transition(ConversationState.ASSISTANT_LISTENING, reason)

    def on_user_speaking(self, text: str, reason: str = "user_speaking") -> None:
        with self._lock:
            self._last_user_text = (text or "").strip() or self._last_user_text
            self._transition(ConversationState.USER_SPEAKING, reason)

    def on_user_speak_end(self, text: str, reason: str = "user_speak_end") -> None:
        with self._lock:
            self._last_user_text = (text or "").strip() or self._last_user_text
            self._transition(ConversationState.USER_SPEAK_END, reason)

    def on_assistant_thinking(self, user_text: str = "") -> None:
        with self._lock:
            if user_text.strip():
                self._last_user_text = user_text.strip()
            self._transition(ConversationState.ASSISTANT_THINKING, "assistant_thinking")

    def on_assistant_speaking(self, assistant_text: str = "", duration_seconds: float | None = None) -> None:
        with self._lock:
            self._cancel_speak_timer()
            if assistant_text.strip():
                self._last_assistant_text = assistant_text.strip()
            self._transition(ConversationState.ASSISTANT_SPEAKING, "assistant_speaking")
            if duration_seconds is not None and duration_seconds > 0:
                timer = threading.Timer(duration_seconds + 0.12, self._handle_estimated_speak_end)
                timer.daemon = True
                self._speak_timer = timer
                timer.start()

    def on_assistant_speak_end(self, reason: str = "assistant_speak_end") -> None:
        with self._lock:
            self._cancel_speak_timer()
            self._transition(ConversationState.ASSISTANT_SPEAK_END, reason)

    def on_assistant_text(self, text: str) -> None:
        with self._lock:
            if text.strip():
                self._last_assistant_text = text.strip()
                self._updated_at = time.monotonic()

    def _handle_estimated_speak_end(self) -> None:
        with self._lock:
            if self._state != ConversationState.ASSISTANT_SPEAKING:
                return
            self._transition(ConversationState.ASSISTANT_SPEAK_END, "assistant_speak_end_estimated")

    def _cancel_speak_timer(self) -> None:
        if self._speak_timer is not None:
            self._speak_timer.cancel()
            self._speak_timer = None

    def _transition(self, next_state: ConversationState, reason: str) -> None:
        previous = self._state
        if previous == next_state:
            self._updated_at = time.monotonic()
            return
        self._state = next_state
        self._updated_at = time.monotonic()
        self._logger.info(
            "session_id=%s state %s -> %s reason=%s",
            self._session_id,
            previous.value,
            next_state.value,
            reason,
        )
