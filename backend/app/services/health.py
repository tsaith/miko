from __future__ import annotations

from app.generated import assistant_pb2, assistant_pb2_grpc


class HealthService(assistant_pb2_grpc.HealthServiceServicer):
    def Ping(self, request: assistant_pb2.PingRequest, context) -> assistant_pb2.PingResponse:
        del request, context
        return assistant_pb2.PingResponse(
            service="miko-backend",
            version="0.1.0",
            status="SERVING",
        )
