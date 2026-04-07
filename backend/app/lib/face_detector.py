from __future__ import annotations

from dataclasses import dataclass
import math
from pathlib import Path

import cv2
import numpy as np

smooth_alpha = 0.35
min_motion_size = 10
min_iou = 0.15

area_history_len = 15
min_history_for_filter = 5
max_area_ratio = 2.5
max_center_jump_frac = 0.20
absent_timeout_frames = 60


@dataclass(slots=True)
class FloatRect:
    x: float
    y: float
    x2: float
    y2: float

    @classmethod
    def from_rect(cls, rect: tuple[int, int, int, int]) -> "FloatRect":
        x, y, w, h = rect
        return cls(float(x), float(y), float(x + w), float(y + h))

    def to_rect(self) -> tuple[int, int, int, int]:
        x = int(round(self.x))
        y = int(round(self.y))
        x2 = int(round(self.x2))
        y2 = int(round(self.y2))
        return x, y, max(0, x2 - x), max(0, y2 - y)

    def lerp(self, target: tuple[int, int, int, int], alpha: float) -> "FloatRect":
        other = FloatRect.from_rect(target)
        return FloatRect(
            x=self.x + alpha * (other.x - self.x),
            y=self.y + alpha * (other.y - self.y),
            x2=self.x2 + alpha * (other.x2 - self.x2),
            y2=self.y2 + alpha * (other.y2 - self.y2),
        )


class FaceDetector:
    def __init__(self, cascade_path: Path | str) -> None:
        self._classifier = cv2.CascadeClassifier(str(cascade_path))
        if self._classifier.empty():
            raise RuntimeError(f"failed to load haar cascade: {cascade_path}")

        self._prev_gray: np.ndarray | None = None
        self._tracked: FloatRect | None = None
        self._img_w = 0
        self._img_h = 0
        self._area_history: list[int] = []
        self._miss_frames = 0
        self._face_present = False
        self._last_motion_rect: tuple[int, int, int, int] | None = None
        self._full_frame_scan_interval = 2
        self._frame_counter = 0

    def close(self) -> None:
        self._prev_gray = None
        self._tracked = None
        self._area_history.clear()
        self._last_motion_rect = None
        self._miss_frames = 0
        self._face_present = False
        self._frame_counter = 0

    def detect(self, frame: np.ndarray) -> tuple[tuple[int, int, int, int] | None, bool]:
        self._img_h, self._img_w = frame.shape[:2]
        self._frame_counter += 1
        gray = cv2.cvtColor(frame, cv2.COLOR_BGR2GRAY)

        if self._prev_gray is None or self._prev_gray.shape != gray.shape:
            self._prev_gray = gray.copy()
            self._last_motion_rect = None
            self._tracked = None
            self._miss_frames = 0
            self._face_present = False
            return None, False

        diff = cv2.absdiff(gray, self._prev_gray)
        _, thresh = cv2.threshold(diff, 25, 255, cv2.THRESH_BINARY)
        kernel = cv2.getStructuringElement(cv2.MORPH_RECT, (15, 15))
        thresh = cv2.dilate(thresh, kernel, iterations=1)
        self._prev_gray = gray.copy()

        motion_rect = self._motion_bounding_rect(thresh)
        self._last_motion_rect = motion_rect
        has_motion = motion_rect is not None and motion_rect[2] > min_motion_size and motion_rect[3] > min_motion_size

        raw_detections: list[tuple[int, int, int, int]] = []
        if has_motion and motion_rect is not None:
            raw_detections = self._detect_in_region(gray, motion_rect)
        elif self._tracked is not None:
            return self._tracked.to_rect(), True
        elif self._should_scan_full_frame():
            raw_detections = self._detect_in_region(gray, (0, 0, self._img_w, self._img_h))

        if not raw_detections and self._tracked is None and self._should_scan_full_frame():
            raw_detections = self._detect_in_region(gray, (0, 0, self._img_w, self._img_h))

        best = self._largest_rect(raw_detections)
        if best is None or not self._is_plausible_detection(best):
            self._miss_frames += 1
            if self._miss_frames >= absent_timeout_frames:
                self._tracked = None
                self._face_present = False
                self._area_history.clear()
                return None, False
            if self._tracked is not None:
                return self._tracked.to_rect(), True
            return None, False

        self._miss_frames = 0
        self._face_present = True

        if self._tracked is None:
            self._tracked = FloatRect.from_rect(best)
        else:
            iou = self._iou_rect(self._tracked.to_rect(), best)
            if iou >= min_iou:
                self._tracked = self._tracked.lerp(best, smooth_alpha)
            else:
                self._tracked = FloatRect.from_rect(best)

        self._push_area(best[2] * best[3])
        return self._tracked.to_rect(), True

    def normalized_position(self) -> tuple[float, float, bool]:
        if self._tracked is None or self._img_w <= 0 or self._img_h <= 0:
            return 0.0, 0.0, False
        cx = (self._tracked.x + self._tracked.x2) / 2.0
        cy = (self._tracked.y + self._tracked.y2) / 2.0
        x = max(0.0, min(1.0, cx / float(self._img_w)))
        y = max(0.0, min(1.0, cy / float(self._img_h)))
        return x, y, True

    def is_present(self) -> bool:
        return self._face_present

    def miss_frames(self) -> int:
        return self._miss_frames

    def motion_bounding_rect(self) -> tuple[int, int, int, int] | None:
        return self._last_motion_rect

    def _should_scan_full_frame(self) -> bool:
        return self._tracked is None and (self._frame_counter % self._full_frame_scan_interval == 0)

    def _detect_in_region(
        self, gray: np.ndarray, rect: tuple[int, int, int, int]
    ) -> list[tuple[int, int, int, int]]:
        x, y, w, h = self._clamp_rect(rect, self._img_w, self._img_h)
        if w <= 0 or h <= 0:
            return []
        roi = gray[y : y + h, x : x + w]
        detected = self._classifier.detectMultiScale(
            roi,
            scaleFactor=1.1,
            minNeighbors=5,
            minSize=(60, 60),
        )
        return [(x + int(dx), y + int(dy), int(dw), int(dh)) for dx, dy, dw, dh in detected]

    def _push_area(self, area: int) -> None:
        if len(self._area_history) >= area_history_len:
            self._area_history.pop(0)
        self._area_history.append(area)

    def _rolling_avg_area(self) -> tuple[float, int]:
        count = len(self._area_history)
        if count == 0:
            return 0.0, 0
        return sum(self._area_history) / float(count), count

    def _is_plausible_detection(self, candidate: tuple[int, int, int, int]) -> bool:
        if len(self._area_history) < min_history_for_filter:
            return True

        avg_area, _ = self._rolling_avg_area()
        cand_area = float(candidate[2] * candidate[3])
        if cand_area > avg_area * max_area_ratio:
            return False
        if cand_area < avg_area / max_area_ratio:
            return False

        if self._tracked is not None and self._img_w > 0 and self._img_h > 0:
            diag = math.sqrt(float(self._img_w * self._img_w + self._img_h * self._img_h))
            max_jump = max_center_jump_frac * diag
            scx = (self._tracked.x + self._tracked.x2) / 2.0
            scy = (self._tracked.y + self._tracked.y2) / 2.0
            ccx = candidate[0] + candidate[2] / 2.0
            ccy = candidate[1] + candidate[3] / 2.0
            dx = ccx - scx
            dy = ccy - scy
            jump = math.sqrt(dx * dx + dy * dy)
            if jump > max_jump:
                return False

        return True

    def _motion_bounding_rect(self, mask: np.ndarray) -> tuple[int, int, int, int] | None:
        contours, _ = cv2.findContours(mask, cv2.RETR_EXTERNAL, cv2.CHAIN_APPROX_SIMPLE)
        union: tuple[int, int, int, int] | None = None
        for contour in contours:
            x, y, w, h = cv2.boundingRect(contour)
            if union is None:
                union = (int(x), int(y), int(w), int(h))
                continue
            union = self._union_rect(union, (int(x), int(y), int(w), int(h)))
        return union

    def _largest_rect(self, rects: list[tuple[int, int, int, int]]) -> tuple[int, int, int, int] | None:
        if not rects:
            return None
        return max(rects, key=lambda rect: rect[2] * rect[3])

    def _clamp_rect(self, rect: tuple[int, int, int, int], img_w: int, img_h: int) -> tuple[int, int, int, int]:
        x, y, w, h = rect
        x1 = max(0, min(img_w, x))
        y1 = max(0, min(img_h, y))
        x2 = max(x1, min(img_w, x + w))
        y2 = max(y1, min(img_h, y + h))
        return x1, y1, x2 - x1, y2 - y1

    def _union_rect(
        self, left: tuple[int, int, int, int], right: tuple[int, int, int, int]
    ) -> tuple[int, int, int, int]:
        x1 = min(left[0], right[0])
        y1 = min(left[1], right[1])
        x2 = max(left[0] + left[2], right[0] + right[2])
        y2 = max(left[1] + left[3], right[1] + right[3])
        return x1, y1, x2 - x1, y2 - y1

    def _iou_rect(
        self, left: tuple[int, int, int, int], right: tuple[int, int, int, int]
    ) -> float:
        lx1, ly1, lw, lh = left
        rx1, ry1, rw, rh = right
        lx2 = lx1 + lw
        ly2 = ly1 + lh
        rx2 = rx1 + rw
        ry2 = ry1 + rh

        inter_x1 = max(lx1, rx1)
        inter_y1 = max(ly1, ry1)
        inter_x2 = min(lx2, rx2)
        inter_y2 = min(ly2, ry2)
        if inter_x2 <= inter_x1 or inter_y2 <= inter_y1:
            return 0.0

        inter_area = float((inter_x2 - inter_x1) * (inter_y2 - inter_y1))
        left_area = float(lw * lh)
        right_area = float(rw * rh)
        union_area = left_area + right_area - inter_area
        if union_area <= 0:
            return 0.0
        return inter_area / union_area
