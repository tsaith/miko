from __future__ import annotations

import math
import random
import struct

from .types import AvatarExpressionFrame

blink_close_duration = 0.06
blink_open_duration = 0.14


def _clamp(value: float, minimum: float, maximum: float) -> float:
    if value < minimum:
        return minimum
    if value > maximum:
        return maximum
    return value


def _random_blink_interval() -> float:
    return 3 + random.random() * 5


def _normalized_rms(pcm_s16le: bytes) -> float:
    if len(pcm_s16le) < 2:
        return 0.0
    sample_count = len(pcm_s16le) // 2
    samples = struct.unpack(f"<{sample_count}h", pcm_s16le[: sample_count * 2])
    total = 0.0
    for sample in samples:
        normalized = float(sample) / 32768.0
        total += normalized * normalized
    rms = math.sqrt(total / sample_count)
    return _clamp(rms * 4.2, 0.0, 1.0)


class ExpressionManager:
    def __init__(self) -> None:
        self.reset()

    def reset(self) -> None:
        self.target_mouth = 0.0
        self.current_mouth = 0.0
        self.blink_timer = _random_blink_interval()
        self.blink_phase = "idle"
        self.blink_phase_time = 0.0
        self.blink_value = 0.0
        self.speech_levels: list[float] = []
        self.speech_cursor = 0
        self.speech_frame_duration = 0.02
        self.speech_frame_elapsed = 0.0
        self.state = "idle"

    def enqueue_speech_audio(self, pcm_s16le: bytes, sample_rate: int, channels: int) -> None:
        frame_duration = 0.02
        frame_samples = max(1, int(sample_rate * frame_duration))
        frame_bytes = frame_samples * max(1, channels) * 2
        levels: list[float] = []
        for offset in range(0, len(pcm_s16le), frame_bytes):
            chunk = pcm_s16le[offset : offset + frame_bytes]
            if not chunk:
                continue
            levels.append(_normalized_rms(chunk))
        self.speech_levels = levels
        self.speech_cursor = 0
        self.speech_frame_duration = frame_duration
        self.speech_frame_elapsed = 0.0

    def tick(self, delta: float, state: str) -> AvatarExpressionFrame:
        if state != self.state:
            self.state = state
            if self.blink_phase == "idle":
                self.blink_timer = self._blink_interval_for(state)
        self._tick_lip_sync(delta, state)
        self._tick_blink(delta, state)
        return AvatarExpressionFrame(
            aa=self.current_mouth,
            blink=self.blink_value,
        )

    def _tick_lip_sync(self, delta: float, state: str) -> None:
        if self.speech_levels:
            self.speech_frame_elapsed += delta
            while self.speech_cursor < len(self.speech_levels)-1 and self.speech_frame_elapsed >= self.speech_frame_duration:
                self.speech_cursor += 1
                self.speech_frame_elapsed -= self.speech_frame_duration

            if self.speech_cursor < len(self.speech_levels):
                self.target_mouth = self.speech_levels[self.speech_cursor]
            else:
                self.target_mouth = 0.0

            if self.speech_cursor >= len(self.speech_levels)-1 and self.speech_frame_elapsed >= self.speech_frame_duration:
                self.speech_levels = []
                self.speech_cursor = 0
                self.speech_frame_elapsed = 0.0
                self.target_mouth = 0.0
        else:
            self.target_mouth = 0.0
        smoothing = 0.42 if state == "speak" else 0.24 if state == "think" else 0.3
        self.current_mouth += (self.target_mouth - self.current_mouth) * smoothing

    def _tick_blink(self, delta: float, state: str) -> None:
        if self.blink_phase == "idle":
            self.blink_timer -= delta
            self.blink_value = 0.0
            if self.blink_timer <= 0:
                self.blink_phase = "closing"
                self.blink_phase_time = 0.0
            return

        self.blink_phase_time += delta
        if self.blink_phase == "closing":
            t = _clamp(self.blink_phase_time / blink_close_duration, 0.0, 1.0)
            self.blink_value = t * t
            if t >= 1.0:
                self.blink_phase = "opening"
                self.blink_phase_time = 0.0
            return

        t = _clamp(self.blink_phase_time / blink_open_duration, 0.0, 1.0)
        self.blink_value = (1.0 - t) * (1.0 - t)
        if t >= 1.0:
            self.blink_phase = "idle"
            self.blink_phase_time = 0.0
            self.blink_value = 0.0
            self.blink_timer = self._blink_interval_for(state)

    def _blink_interval_for(self, state: str) -> float:
        if state == "speak":
            return 4.5 + random.random() * 3.0
        if state == "listen":
            return 3.5 + random.random() * 3.0
        if state == "think":
            return 2.6 + random.random() * 2.4
        return _random_blink_interval()
