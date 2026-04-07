from __future__ import annotations

import logging

import numpy as np
import torch
from silero_vad import get_speech_timestamps, load_silero_vad


class VADService:
    """
    Voice Activity Detection service using Silero VAD.
    """

    BUFFER_SIZE = 512

    def __init__(self, sample_rate: int = 16000, threshold: float = 0.5) -> None:
        self.sample_rate = sample_rate
        self.threshold = threshold
        self._logger = logging.getLogger("miko.backend.vad")
        self.model = load_silero_vad()

    def frame_samples(self, sample_rate: int | None = None) -> int:
        rate = sample_rate or self.sample_rate
        if rate == 16000:
            return self.BUFFER_SIZE
        if rate == 8000:
            return self.BUFFER_SIZE // 2
        raise ValueError(f"unsupported silero sample rate: {rate}")

    def frame_bytes(self, sample_rate: int | None = None, channels: int = 1) -> int:
        return self.frame_samples(sample_rate) * max(1, channels) * 2

    def is_speech(self, pcm_s16le: bytes, sample_rate: int | None = None, channels: int = 1) -> bool:
        audio_chunk = self._pcm_to_float32(pcm_s16le, channels)
        if audio_chunk.size == 0:
            return False

        rate = sample_rate or self.sample_rate
        audio_tensor = torch.from_numpy(audio_chunk).float()
        if audio_tensor.ndim > 1:
            audio_tensor = audio_tensor.mean(dim=1)

        speech_prob = float(self.model(audio_tensor, rate).item())
        return speech_prob > self.threshold

    def get_timestamps(
        self,
        pcm_s16le: bytes,
        sample_rate: int | None = None,
        channels: int = 1,
    ) -> list[dict[str, int]]:
        audio_data = self._pcm_to_float32(pcm_s16le, channels)
        if audio_data.size == 0:
            return []
        audio_tensor = torch.from_numpy(audio_data).float()
        if audio_tensor.ndim > 1:
            audio_tensor = audio_tensor.mean(dim=1)
        return get_speech_timestamps(
            audio_tensor,
            self.model,
            sampling_rate=sample_rate or self.sample_rate,
        )

    def _pcm_to_float32(self, pcm_s16le: bytes, channels: int) -> np.ndarray:
        if len(pcm_s16le) < 2:
            return np.array([], dtype=np.float32)
        audio = np.frombuffer(pcm_s16le, dtype=np.int16).astype(np.float32) / 32768.0
        if channels > 1:
            audio = audio.reshape(-1, channels).mean(axis=1)
        return audio
