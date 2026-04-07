from __future__ import annotations

from dataclasses import dataclass
import math


def rounded(value: float) -> float:
    return math.floor(value * 1_000_000 + 0.5) / 1_000_000


@dataclass(slots=True)
class Vector3Frame:
    x: float
    y: float
    z: float

    def to_dict(self) -> dict[str, float]:
        return {"x": rounded(self.x), "y": rounded(self.y), "z": rounded(self.z)}


@dataclass(slots=True)
class AvatarWindFrame:
    intensity: float
    gravity: Vector3Frame
    head: Vector3Frame
    neck: Vector3Frame

    def to_dict(self) -> dict[str, object]:
        return {
            "intensity": rounded(self.intensity),
            "gravity": self.gravity.to_dict(),
            "head": self.head.to_dict(),
            "neck": self.neck.to_dict(),
        }


@dataclass(slots=True)
class AvatarExpressionFrame:
    aa: float
    blink: float

    def to_dict(self) -> dict[str, float]:
        return {
            "aa": rounded(self.aa),
            "blink": rounded(self.blink),
        }


@dataclass(slots=True)
class AvatarMotionFrame:
    version: int
    sequence: int
    pose: dict[str, object]
    expressions: AvatarExpressionFrame

    def to_dict(self) -> dict[str, object]:
        return {
            "version": self.version,
            "sequence": self.sequence,
            "pose": self.pose,
            "expressions": self.expressions.to_dict(),
        }
