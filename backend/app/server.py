from __future__ import annotations

import logging
import os
import signal
import sys
import threading
from concurrent import futures

import grpc
from grpc_health.v1 import health, health_pb2, health_pb2_grpc

from .config import load_settings
from .services.avatar.motion_controller import MotionController
from .services.conversation import ConversationService
from .services.event_bus import EventBus
from .services.health import HealthService
from .services.vision.service import VisionService
from .generated import assistant_pb2_grpc


def configure_logging() -> None:
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s [%(levelname)s] %(name)s %(message)s",
        stream=sys.stdout,
    )


def serve() -> None:
    configure_logging()
    logger = logging.getLogger("miko.backend")
    settings = load_settings()
    event_bus = EventBus()
    motion_controller = MotionController(event_bus)
    motion_controller.start()
    vision_service = VisionService(settings, event_bus, motion_controller)
    shutdown_event = threading.Event()
    shutdown_lock = threading.Lock()
    executor = futures.ThreadPoolExecutor(max_workers=8, thread_name_prefix="miko-grpc")

    server = grpc.server(executor)
    health_servicer = health.HealthServicer(experimental_non_blocking=True)
    health_servicer.set("", health_pb2.HealthCheckResponse.SERVING)
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)
    assistant_pb2_grpc.add_HealthServiceServicer_to_server(HealthService(), server)
    assistant_pb2_grpc.add_ConversationServiceServicer_to_server(ConversationService(settings, event_bus, motion_controller), server)
    assistant_pb2_grpc.add_VisionServiceServicer_to_server(vision_service, server)

    address = settings.listen_target()
    if settings.socket_path is not None:
        settings.socket_path.parent.mkdir(parents=True, exist_ok=True)
        try:
            os.remove(settings.socket_path)
        except FileNotFoundError:
            pass
    server.add_insecure_port(address)
    server.start()
    logger.info("python backend sidecar listening on %s", address)

    def shutdown(*_: object) -> None:
        with shutdown_lock:
            if shutdown_event.is_set():
                return
            shutdown_event.set()
        logger.info("python backend sidecar shutting down")
        try:
            server.stop(grace=2).wait(3)
        except Exception as exc:  # pragma: no cover - defensive cleanup
            logger.warning("grpc server stop failed: %s", exc)

    signal.signal(signal.SIGINT, shutdown)
    signal.signal(signal.SIGTERM, shutdown)

    try:
        while not shutdown_event.is_set():
            server.wait_for_termination(timeout=0.5)
    except KeyboardInterrupt:
        shutdown()
    finally:
        vision_service.stop()
        motion_controller.stop()
        executor.shutdown(wait=True, cancel_futures=True)
        if settings.socket_path is not None:
            try:
                os.remove(settings.socket_path)
            except FileNotFoundError:
                pass
        logger.info("python backend sidecar stopped")


if __name__ == "__main__":
    serve()
