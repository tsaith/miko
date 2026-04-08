from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path

import numpy as np

try:
    from ultralytics import YOLO
except ImportError as exc:  # pragma: no cover - runtime dependency guard
    raise RuntimeError("ultralytics is required for YoloFaceDetector") from exc


@dataclass(slots=True)
class FloatRect:
    x: float
    y: float
    x2: float
    y2: float

    @classmethod
    def from_xyxy(cls, xyxy: tuple[float, float, float, float]) -> "FloatRect":
        x1, y1, x2, y2 = xyxy
        return cls(x1, y1, x2, y2)

    def to_rect(self) -> tuple[int, int, int, int]:
        x1 = int(round(self.x))
        y1 = int(round(self.y))
        x2 = int(round(self.x2))
        y2 = int(round(self.y2))
        return x1, y1, max(0, x2 - x1), max(0, y2 - y1)

    def lerp(self, target: tuple[float, float, float, float], alpha: float) -> "FloatRect":
        tx1, ty1, tx2, ty2 = target
        return FloatRect(
            x=self.x + alpha * (tx1 - self.x),
            y=self.y + alpha * (ty1 - self.y),
            x2=self.x2 + alpha * (tx2 - self.x2),
            y2=self.y2 + alpha * (ty2 - self.y2),
        )


class YoloFaceDetector:
    def __init__(
        self,
        model_path: Path | str,
        *,
        conf_threshold: float = 0.35,
        image_size: int = 640,
        device: str = "cpu",
        smoothing_alpha: float = 0.35,
        absent_timeout_frames: int = 10,
    ) -> None:
        self._model_path = Path(model_path)
        self._model = YOLO(str(self._model_path))
        self._conf_threshold = conf_threshold
        self._image_size = image_size
        self._device = device
        self._smoothing_alpha = smoothing_alpha
        self._absent_timeout_frames = absent_timeout_frames

        self._tracked: FloatRect | None = None
        self._img_w = 0
        self._img_h = 0
        self._face_present = False
        self._miss_frames = 0
        self._confidence = 0.0

    def close(self) -> None:
        self._tracked = None
        self._img_w = 0
        self._img_h = 0
        self._face_present = False
        self._miss_frames = 0
        self._confidence = 0.0

    def detect(self, frame: np.ndarray) -> tuple[tuple[int, int, int, int] | None, bool]:
        self._img_h, self._img_w = frame.shape[:2]

        results = self._model.predict(
            source=frame,
            conf=self._conf_threshold,
            imgsz=self._image_size,
            verbose=False,
            device=self._device,
        )
        best = self._best_detection(results, self._img_w, self._img_h)
        if best is None:
            self._miss_frames += 1
            self._confidence = 0.0
            if self._miss_frames >= self._absent_timeout_frames:
                self._tracked = None
                self._face_present = False
                return None, False
            if self._tracked is not None:
                return self._tracked.to_rect(), True
            return None, False

        self._miss_frames = 0
        self._face_present = True

        xyxy, conf = best
        self._confidence = conf
        if self._tracked is None:
            self._tracked = FloatRect.from_xyxy(xyxy)
        else:
            self._tracked = self._tracked.lerp(xyxy, self._smoothing_alpha)

        return self._tracked.to_rect(), True

    def normalized_position(self) -> tuple[float, float, bool]:
        if self._tracked is None or self._img_w <= 0 or self._img_h <= 0:
            return 0.0, 0.0, False
        cx = (self._tracked.x + self._tracked.x2) / 2.0
        cy = (self._tracked.y + self._tracked.y2) / 2.0
        return (
            max(0.0, min(1.0, cx / float(self._img_w))),
            max(0.0, min(1.0, cy / float(self._img_h))),
            True,
        )

    def is_present(self) -> bool:
        return self._face_present

    def miss_frames(self) -> int:
        return self._miss_frames

    def confidence(self) -> float:
        return self._confidence

    def _best_detection(
        self,
        results: list,
        frame_w: int,
        frame_h: int,
    ) -> tuple[tuple[float, float, float, float], float] | None:
        if not results:
            return None

        result = results[0]
        boxes = getattr(result, "boxes", None)
        if boxes is None or len(boxes) == 0:
            return None

        best_score = -1.0
        best_xyxy: tuple[float, float, float, float] | None = None
        best_conf = 0.0

        for box in boxes:
            conf = float(box.conf[0]) if box.conf is not None else 0.0
            x1, y1, x2, y2 = [float(v) for v in box.xyxy[0].tolist()]
            x1 = max(0.0, min(float(frame_w - 1), x1))
            y1 = max(0.0, min(float(frame_h - 1), y1))
            x2 = max(x1 + 1.0, min(float(frame_w), x2))
            y2 = max(y1 + 1.0, min(float(frame_h), y2))
            area = max(1.0, (x2 - x1) * (y2 - y1))
            score = area * max(0.001, conf)
            if score > best_score:
                best_score = score
                best_xyxy = (x1, y1, x2, y2)
                best_conf = conf

        if best_xyxy is None:
            return None
        return best_xyxy, best_conf
