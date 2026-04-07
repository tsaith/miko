from __future__ import annotations

import queue
import threading
from typing import Iterable

from app.generated import assistant_pb2


class EventBus:
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._subscribers: set[queue.Queue[assistant_pb2.BackendEvent]] = set()

    def publish(self, event: assistant_pb2.BackendEvent) -> None:
        with self._lock:
            subscribers = list(self._subscribers)
        for subscriber in subscribers:
            try:
                subscriber.put_nowait(event)
            except queue.Full:
                pass

    def subscribe(self) -> queue.Queue[assistant_pb2.BackendEvent]:
        subscriber: queue.Queue[assistant_pb2.BackendEvent] = queue.Queue(maxsize=128)
        with self._lock:
            self._subscribers.add(subscriber)
        return subscriber

    def unsubscribe(self, subscriber: queue.Queue[assistant_pb2.BackendEvent]) -> None:
        with self._lock:
            self._subscribers.discard(subscriber)

    def subscribers(self) -> Iterable[queue.Queue[assistant_pb2.BackendEvent]]:
        with self._lock:
            return tuple(self._subscribers)
