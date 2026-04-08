from __future__ import annotations

import json
import logging
import threading
import time

from app.generated import assistant_pb2
from app.services.event_bus import EventBus

from .expression_manager import ExpressionManager
from .pose_manager import PoseManager
from .types import AvatarMotionFrame


class MotionController:
    def __init__(self, event_bus: EventBus) -> None:
        self._event_bus = event_bus
        self._logger = logging.getLogger("miko.backend.avatar.motion")
        self._lock = threading.Lock()
        self._pose = PoseManager()
        self._expressions = ExpressionManager()
        self._sequence = 0
        self._state = "idle"
        self._phase = "idle"
        self._conversation_active = False
        self._face_present = False
        self._face_x = 0.5
        self._face_y = 0.5
        self._last_face_seen_at = 0.0
        self._face_hold_seconds = 0.55
        self._speak_until = 0.0
        self._worker: threading.Thread | None = None
        self._stop_event = threading.Event()

    def start(self) -> None:
        with self._lock:
            if self._worker and self._worker.is_alive():
                return
            self._stop_event.clear()
            self._worker = threading.Thread(target=self._loop, name="miko-avatar-motion", daemon=True)
            self._worker.start()
        self._logger.info("motion loop started")

    def stop(self) -> None:
        with self._lock:
            worker = self._worker
            self._stop_event.set()
        if worker and worker.is_alive():
            worker.join(timeout=2)
        with self._lock:
            self._worker = None
        self._logger.info("motion loop stopped")

    def reset(self) -> None:
        with self._lock:
            self._sequence = 0
            self._state = "idle"
            self._phase = "idle"
            self._conversation_active = False
            self._face_present = False
            self._face_x = 0.5
            self._face_y = 0.5
            self._last_face_seen_at = 0.0
            self._speak_until = 0.0
            self._pose.reset()
            self._expressions.reset()

    def set_conversation_active(self, active: bool) -> None:
        with self._lock:
            if self._conversation_active != active:
                self._logger.info("conversation_active %s -> %s", self._conversation_active, active)
            self._conversation_active = active
            if not active:
                self._phase = "idle"
            elif self._phase == "idle":
                self._phase = "listen"

    def set_phase(self, phase: str) -> None:
        next_phase = phase.strip().lower()
        if next_phase not in {"idle", "listen", "think", "speak"}:
            raise ValueError(f"unsupported motion phase: {phase}")
        with self._lock:
            if self._phase != next_phase:
                self._logger.info("phase transition %s -> %s", self._phase, next_phase)
            self._phase = next_phase
            if next_phase != "speak":
                self._speak_until = 0.0

    def begin_speaking(self, duration_seconds: float) -> None:
        duration = max(0.0, duration_seconds)
        tail = 0.12
        with self._lock:
            if self._phase != "speak":
                self._logger.info("phase transition %s -> speak duration=%.3fs", self._phase, duration)
            else:
                self._logger.info("phase extend speak duration=%.3fs", duration)
            self._phase = "speak"
            self._speak_until = time.monotonic() + duration + tail

    def end_speaking(self, reason: str = "playback_end") -> None:
        with self._lock:
            if self._phase != "speak" and self._speak_until <= 0:
                return
            next_phase = "listen" if self._conversation_active else "idle"
            self._logger.info("phase transition %s -> %s reason=%s", self._phase, next_phase, reason)
            self._phase = next_phase
            self._speak_until = 0.0
            self._expressions.stop_speaking(immediate=reason.startswith("playback_interrupt"))

    def set_face_target(self, x: float, y: float, present: bool) -> None:
        with self._lock:
            if present:
                self._face_present = True
                self._face_x = x
                self._face_y = y
                self._last_face_seen_at = time.monotonic()
                self._pose.set_face_target(x, y, True)
            else:
                self._face_present = False

    def enqueue_speech_audio(self, pcm_s16le: bytes, sample_rate: int, channels: int) -> None:
        with self._lock:
            self._expressions.enqueue_speech_audio(pcm_s16le, sample_rate, channels)

    def _loop(self) -> None:
        last = time.monotonic()
        while not self._stop_event.is_set():
            now = time.monotonic()
            delta = min(now - last, 0.1)
            last = now

            payload = None
            with self._lock:
                if self._phase == "speak" and self._speak_until > 0 and now >= self._speak_until:
                    next_phase = "listen" if self._conversation_active else "idle"
                    self._logger.info("phase transition speak -> %s reason=playback_elapsed", next_phase)
                    self._phase = next_phase
                    self._speak_until = 0.0
                    self._expressions.stop_speaking(immediate=False)
                allow_face_tracking = self._face_available(now)
                desired_state = self._resolve_state(allow_face_tracking)
                if desired_state != self._state:
                    self._logger.info(
                        "state transition %s -> %s phase=%s face=%s",
                        self._state,
                        desired_state,
                        self._phase,
                        allow_face_tracking,
                    )
                    self._state = desired_state
                pose = self._pose.tick(delta, self._state, allow_face_tracking)
                expressions = self._expressions.tick(delta, self._state)
                self._sequence += 1
                frame = AvatarMotionFrame(
                    version=1,
                    sequence=self._sequence,
                    pose=pose,
                    expressions=expressions,
                )
                payload = json.dumps(frame.to_dict(), separators=(",", ":"))

            self._event_bus.publish(
                assistant_pb2.BackendEvent(
                    avatar_motion=assistant_pb2.AvatarMotionEvent(json=payload),
                )
            )
            time.sleep(max(0.0, (1.0 / 30.0) - (time.monotonic() - now)))

    def _face_available(self, now: float) -> bool:
        if self._face_present:
            return True
        if self._last_face_seen_at <= 0:
            return False
        return (now - self._last_face_seen_at) <= self._face_hold_seconds

    def _resolve_state(self, allow_face_tracking: bool) -> str:
        if self._phase == "speak":
            return "speak"
        if self._phase == "think":
            return "think"
        if self._phase == "listen" and self._conversation_active and allow_face_tracking:
            return "listen"
        if self._conversation_active and allow_face_tracking:
            return "listen"
        return "idle"
