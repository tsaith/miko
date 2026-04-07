from __future__ import annotations

import argparse
from dataclasses import dataclass
from pathlib import Path
import sys
import time

import cv2


BACKEND_ROOT = Path(__file__).resolve().parents[1]
if str(BACKEND_ROOT) not in sys.path:
    sys.path.insert(0, str(BACKEND_ROOT))

from app.config import load_settings
from app.lib.face_detector import FaceDetector


@dataclass(slots=True)
class PerfStats:
    fps: float = 0.0
    detect_ms: float = 0.0
    frame_count: int = 0

    def update(self, frame_dt: float, detect_dt: float) -> None:
        self.frame_count += 1
        if frame_dt > 0:
            instant_fps = 1.0 / frame_dt
            self.fps = instant_fps if self.fps <= 0 else self.fps * 0.9 + instant_fps * 0.1
        self.detect_ms = detect_dt if self.detect_ms <= 0 else self.detect_ms * 0.85 + detect_dt * 0.15


@dataclass(slots=True)
class FaceObservation:
    has_face: bool
    face_rect: tuple[int, int, int, int] | None
    motion_rect: tuple[int, int, int, int] | None
    normalized_x: float = 0.0
    normalized_y: float = 0.0
    has_normalized_position: bool = False
    present: bool = False
    miss_frames: int = 0


def resolve_cascade_path() -> Path:
    settings = load_settings()
    candidates: list[Path] = []
    if settings.models_dir is not None:
        candidates.append(settings.models_dir / "haarcascade_frontalface_default.xml")

    candidates.extend(
        [
            BACKEND_ROOT.parent / "models" / "haarcascade_frontalface_default.xml",
            BACKEND_ROOT / "models" / "haarcascade_frontalface_default.xml",
            Path(cv2.data.haarcascades) / "haarcascade_frontalface_default.xml",
        ]
    )

    for candidate in candidates:
        if candidate.exists():
            return candidate
    raise FileNotFoundError("cannot locate haarcascade_frontalface_default.xml")


def create_face_detector() -> FaceDetector:
    return FaceDetector(resolve_cascade_path())


def observe_face(detector: FaceDetector, frame) -> FaceObservation:
    face_rect, has_face = detector.detect(frame)
    normalized_x, normalized_y, has_normalized_position = detector.normalized_position()
    return FaceObservation(
        has_face=has_face,
        face_rect=face_rect,
        motion_rect=detector.motion_bounding_rect(),
        normalized_x=normalized_x,
        normalized_y=normalized_y,
        has_normalized_position=has_normalized_position,
        present=detector.is_present(),
        miss_frames=detector.miss_frames(),
    )


def draw_osd_text(frame, text: str, origin: tuple[int, int]) -> None:
    font = cv2.FONT_HERSHEY_SIMPLEX
    scale = 0.65
    thickness = 2
    shadow = (0, 0, 0)
    white = (255, 255, 255)
    cv2.putText(frame, text, (origin[0] + 1, origin[1] + 1), font, scale, shadow, thickness, cv2.LINE_AA)
    cv2.putText(frame, text, origin, font, scale, white, thickness, cv2.LINE_AA)


def main() -> int:
    parser = argparse.ArgumentParser(description="FaceDetector webcam experiment")
    parser.add_argument("--device", type=int, default=0, help="camera device id")
    parser.add_argument("--width", type=int, default=640, help="capture width")
    parser.add_argument("--height", type=int, default=480, help="capture height")
    parser.add_argument("--title", default="Miko Face Detector", help="OpenCV window title")
    args = parser.parse_args()

    cascade_path = resolve_cascade_path()
    detector = create_face_detector()
    capture = cv2.VideoCapture(args.device)
    if not capture.isOpened():
        print(f"cannot open camera device {args.device}")
        return 1

    capture.set(cv2.CAP_PROP_FRAME_WIDTH, args.width)
    capture.set(cv2.CAP_PROP_FRAME_HEIGHT, args.height)
    capture.set(cv2.CAP_PROP_FPS, 30)

    stats = PerfStats()
    last_frame_time = time.perf_counter()
    print(f"start reading camera device: {args.device}  (press 'q' / ESC to quit)")
    print(f"cascade: {cascade_path}")

    try:
        while True:
            ok, frame = capture.read()
            if not ok or frame is None:
                print(f"cannot read device {args.device}")
                return 1

            frame_started_at = time.perf_counter()
            frame_dt = frame_started_at - last_frame_time
            last_frame_time = frame_started_at

            detect_started_at = time.perf_counter()
            observation = observe_face(detector, frame)
            detect_ms = (time.perf_counter() - detect_started_at) * 1000.0
            stats.update(frame_dt, detect_ms)

            if observation.motion_rect is not None and observation.motion_rect[2] > 10 and observation.motion_rect[3] > 10:
                x, y, w, h = observation.motion_rect
                cv2.rectangle(frame, (x, y), (x + w, y + h), (0, 220, 80), 1)

            if observation.has_face and observation.face_rect is not None:
                x, y, w, h = observation.face_rect
                cv2.rectangle(frame, (x, y), (x + w, y + h), (30, 130, 255), 3)

            fps_line = f"FPS: {stats.fps:.1f}  Detect: {stats.detect_ms:.2f} ms"
            pos_line = "Face: --"
            if observation.has_normalized_position:
                pos_line = f"Face: x={observation.normalized_x:.3f}  y={observation.normalized_y:.3f}"
            state_line = f"Present: {str(observation.present).lower()}  Miss: {observation.miss_frames}"

            draw_osd_text(frame, fps_line, (12, 30))
            draw_osd_text(frame, pos_line, (12, 58))
            draw_osd_text(frame, state_line, (12, 86))

            cv2.imshow(args.title, frame)
            key = cv2.waitKey(1) & 0xFF
            if key == ord("q") or key == 27:
                print("quit")
                return 0
    finally:
        capture.release()
        detector.close()
        cv2.destroyAllWindows()


if __name__ == "__main__":
    raise SystemExit(main())
