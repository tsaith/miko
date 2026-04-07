from __future__ import annotations

import math
import struct


class VADService:
    """
    Lightweight VAD for the sidecar pipeline.
    Keeps the same role separation as the reference project, but uses
    a simple RMS-based detector to avoid large runtime dependencies.
    """

    BUFFER_SIZE = 640

    def __init__(self, threshold: float = 420.0) -> None:
        self.threshold = threshold

    def is_speech(self, pcm_s16le: bytes) -> bool:
        return self.rms(pcm_s16le) >= self.threshold

    def rms(self, pcm_s16le: bytes) -> float:
        sample_count = len(pcm_s16le) // 2
        if sample_count == 0:
            return 0.0
        samples = struct.unpack(f"<{sample_count}h", pcm_s16le)
        total = 0.0
        for sample in samples:
            total += float(sample) * float(sample)
        return math.sqrt(total / sample_count)
