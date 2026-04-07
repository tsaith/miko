from __future__ import annotations

import logging
import threading
import time
from pathlib import Path

import cv2

from app.generated import assistant_pb2, assistant_pb2_grpc
from app.config import BackendSettings
from app.services.event_bus import EventBus


class VisionService(assistant_pb2_grpc.VisionServiceServicer):
    def __init__(self, settings: BackendSettings, event_bus: EventBus) -> None:
        self._settings = settings
        self._event_bus = event_bus
        self._logger = logging.getLogger("miko.backend.vision")
        self._worker: threading.Thread | None = None
        self._stop_event = threading.Event()
        self._lock = threading.Lock()

    def StartVision(self, request: assistant_pb2.StartVisionRequest, context) -> assistant_pb2.Ack:
        del request, context
        with self._lock:
            if self._worker and self._worker.is_alive():
                return assistant_pb2.Ack(ok=True, message="vision already running")

            detector = self._create_detector()
            capture = cv2.VideoCapture(0)
            if not capture.isOpened():
                capture.release()
                return assistant_pb2.Ack(ok=False, message="default camera is not available")

            capture.set(cv2.CAP_PROP_FRAME_WIDTH, 640)
            capture.set(cv2.CAP_PROP_FRAME_HEIGHT, 480)
            capture.set(cv2.CAP_PROP_FPS, 30)

            self._stop_event.clear()
            self._worker = threading.Thread(
                target=self._loop,
                name="miko-vision",
                args=(capture, detector),
                daemon=True,
            )
            self._worker.start()

        self._logger.info("vision loop started")
        return assistant_pb2.Ack(ok=True, message="vision started")

    def StopVision(self, request: assistant_pb2.StopVisionRequest, context) -> assistant_pb2.Ack:
        del request, context
        self.stop()
        return assistant_pb2.Ack(ok=True, message="vision stopped")

    def stop(self) -> None:
        with self._lock:
            worker = self._worker
            self._stop_event.set()
        if worker and worker.is_alive():
            worker.join(timeout=2)
        with self._lock:
            self._worker = None

    def _loop(self, capture: cv2.VideoCapture, detector: cv2.CascadeClassifier) -> None:
        try:
            last_present: bool | None = None
            last_x = -1.0
            last_y = -1.0

            while not self._stop_event.is_set():
                ok, frame = capture.read()
                if not ok or frame is None:
                    time.sleep(0.05)
                    continue

                gray = cv2.cvtColor(frame, cv2.COLOR_BGR2GRAY)
                faces = detector.detectMultiScale(
                    gray,
                    scaleFactor=1.1,
                    minNeighbors=5,
                    minSize=(60, 60),
                )

                if len(faces) == 0:
                    if last_present is not False:
                        self._publish_face(False, 0.0, 0.0)
                        last_present = False
                    time.sleep(1 / 15)
                    continue

                x, y, w, h = max(faces, key=lambda rect: rect[2] * rect[3])
                center_x = (x + w / 2) / frame.shape[1]
                center_y = (y + h / 2) / frame.shape[0]

                if (
                    last_present is not True
                    or abs(center_x - last_x) > 0.01
                    or abs(center_y - last_y) > 0.01
                ):
                    self._publish_face(True, center_x, center_y)
                    last_present = True
                    last_x = center_x
                    last_y = center_y

                time.sleep(1 / 15)
        finally:
            capture.release()
            self._logger.info("vision loop stopped")

    def _publish_face(self, present: bool, x: float, y: float) -> None:
        self._event_bus.publish(
            assistant_pb2.BackendEvent(
                face_target=assistant_pb2.FaceTargetEvent(
                    present=present,
                    x=float(x),
                    y=float(y),
                )
            )
        )

    def _create_detector(self) -> cv2.CascadeClassifier:
        cascade_path = self._resolve_cascade_path()
        detector = cv2.CascadeClassifier(str(cascade_path))
        if detector.empty():
            raise RuntimeError(f"failed to load haar cascade: {cascade_path}")
        return detector

    def _resolve_cascade_path(self) -> Path:
        candidates = []
        if self._settings.models_dir is not None:
            candidates.append(self._settings.models_dir / "haarcascade_frontalface_default.xml")

        backend_root = Path(__file__).resolve().parents[3]
        candidates.extend(
            [
                backend_root.parent / "models" / "haarcascade_frontalface_default.xml",
                backend_root / "models" / "haarcascade_frontalface_default.xml",
                Path(cv2.data.haarcascades) / "haarcascade_frontalface_default.xml",
            ]
        )

        for candidate in candidates:
            if candidate.exists():
                return candidate
        raise FileNotFoundError("cannot locate haarcascade_frontalface_default.xml")
