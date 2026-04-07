from __future__ import annotations

import math
import random

from .types import AvatarWindFrame, Vector3Frame, rounded

a_pose_upper_arm = math.pi * 0.3

inhale_duration = 6.0
exhale_duration = 4.0
breath_shoulder = 0.06
breath_chest_z = 0.03

eye_hold_min = 1.5
eye_hold_max = 3.5
eye_max_yaw = 0.038
eye_max_pitch = 0.038
eye_lerp = 0.08
face_eye_lerp = 0.18
head_max_yaw = 0.11
head_max_pitch = 0.11
neck_max_yaw = 0.05
neck_max_pitch = 0.05
head_lerp = 0.14
neck_lerp = 0.12

shift_hold_min = 4.0
shift_hold_max = 8.0
shift_hip_z = 0.015
shift_spine_z = 0.007
shift_lerp = 0.008

wind_base_dir_x = -0.82
wind_base_dir_y = -0.05
wind_base_dir_z = -0.56
wind_head_tilt_z = 0.045
wind_head_turn_y = -0.035
wind_neck_tilt_z = 0.025
wind_shift_min = 1.4
wind_shift_max = 3.4
wind_base_min = 0.2
wind_base_max = 0.42
wind_gust_min = 0.58
wind_gust_max = 0.82
wind_gust_chance = 0.22
wind_response = 2.3


def _random_in(minimum: float, maximum: float) -> float:
    return minimum + random.random() * (maximum - minimum)


def _clamp(value: float, minimum: float, maximum: float) -> float:
    if value < minimum:
        return minimum
    if value > maximum:
        return maximum
    return value


class PoseManager:
    def __init__(self) -> None:
        self.reset()

    def reset(self) -> None:
        self.breath_time = 0.0
        self.eye_timer = _random_in(eye_hold_min, eye_hold_max)
        self.eye_target_y = 0.0
        self.eye_current_x = 0.0
        self.eye_current_y = 0.0
        self.head_current_x = 0.0
        self.head_current_y = 0.0
        self.neck_current_x = 0.0
        self.neck_current_y = 0.0
        self.face_target_x = 0.0
        self.face_target_y = 0.0
        self.face_norm_x = 0.5
        self.face_norm_y = 0.5
        self.face_present = False
        self.shift_timer = _random_in(shift_hold_min, shift_hold_max)
        self.shift_target_z = 0.0
        self.shift_current_z = 0.0
        self.wind_timer = _random_in(wind_shift_min, wind_shift_max)
        self.wind_time = 0.0
        self.wind_current = _random_in(wind_base_min * 0.7, wind_base_max * 0.9)
        self.wind_target = _random_in(wind_base_min, wind_base_max)

    def set_face_target(self, x: float, y: float, present: bool) -> None:
        self.face_present = present
        if not present:
            return
        self.face_norm_x = _clamp(x, 0.0, 1.0)
        self.face_norm_y = _clamp(y, 0.0, 1.0)
        self.face_target_x = _clamp((self.face_norm_y - 0.5) * 2.0 * eye_max_pitch, -eye_max_pitch, eye_max_pitch)
        self.face_target_y = _clamp((0.5 - self.face_norm_x) * 2.0 * eye_max_yaw, -eye_max_yaw, eye_max_yaw)

    def tick(self, delta: float, state: str, allow_face_tracking: bool) -> dict[str, object]:
        self.breath_time += delta
        breath_value = self._breath_value()
        eye_x, eye_y = self._tick_eyes(delta, state, allow_face_tracking)
        hips_z, spine_z = self._tick_weight_shift(delta)
        wind = self._tick_wind(delta, state, allow_face_tracking)

        return {
            "base": {
                "leftUpperArmZ": rounded(-a_pose_upper_arm),
                "rightUpperArmZ": rounded(a_pose_upper_arm),
            },
            "breathing": {
                "value": rounded(breath_value),
                "shoulderZ": rounded(breath_value * breath_shoulder),
                "chestZ": rounded(breath_value * breath_chest_z),
            },
            "gaze": {
                "eyeX": rounded(eye_x),
                "eyeY": rounded(eye_y),
            },
            "weightShift": {
                "hipsZ": rounded(hips_z),
                "spineZ": rounded(spine_z),
            },
            "wind": wind.to_dict(),
            "state": state,
        }

    def _breath_value(self) -> float:
        cycle = inhale_duration + exhale_duration
        phase = math.fmod(self.breath_time, cycle)
        if phase < inhale_duration:
            t = phase / inhale_duration
            return (1.0 - math.cos(t * math.pi)) / 2.0
        t = (phase - inhale_duration) / exhale_duration
        return (1.0 + math.cos(t * math.pi)) / 2.0

    def _tick_eyes(self, delta: float, state: str, allow_face_tracking: bool) -> tuple[float, float]:
        if state in {"idle", "listen", "speak"} and allow_face_tracking and self.face_present:
            target_x = self.face_target_x
            target_y = self.face_target_y
            if state == "idle":
                target_x += eye_max_pitch * 0.02
                target_y *= 0.68
                lerp = face_eye_lerp * 0.58
            elif state == "listen":
                target_x += eye_max_pitch * 0.04
                lerp = face_eye_lerp * 0.9
            else:
                target_x += math.sin(self.wind_time * 6.4) * eye_max_pitch * 0.05
                lerp = face_eye_lerp * 1.15
            self.eye_current_x += (target_x - self.eye_current_x) * lerp
            self.eye_current_y += (target_y - self.eye_current_y) * lerp
            return self.eye_current_x, self.eye_current_y
        if state == "think":
            target_x = -eye_max_pitch * 0.38
            target_y = eye_max_yaw * 0.2
            self.eye_current_x += (target_x - self.eye_current_x) * 0.06
            self.eye_current_y += (target_y - self.eye_current_y) * 0.06
            return self.eye_current_x, self.eye_current_y

        self.eye_timer -= delta
        if self.eye_timer <= 0:
            if random.random() < 0.25:
                self.eye_target_y = 0.0
            else:
                self.eye_target_y = _random_in(-eye_max_yaw, eye_max_yaw)
            self.eye_timer = _random_in(eye_hold_min, eye_hold_max)

        self.eye_current_x += (0.0 - self.eye_current_x) * eye_lerp
        self.eye_current_y += (self.eye_target_y - self.eye_current_y) * eye_lerp
        return self.eye_current_x, self.eye_current_y

    def _tick_weight_shift(self, delta: float) -> tuple[float, float]:
        self.shift_timer -= delta
        if self.shift_timer <= 0:
            roll = random.random()
            if roll < 0.33:
                self.shift_target_z = -shift_hip_z
            elif roll < 0.5:
                self.shift_target_z = 0.0
            else:
                self.shift_target_z = shift_hip_z
            self.shift_timer = _random_in(shift_hold_min, shift_hold_max)

        self.shift_current_z += (self.shift_target_z - self.shift_current_z) * shift_lerp
        spine_z = -self.shift_current_z * (shift_spine_z / shift_hip_z)
        return self.shift_current_z, spine_z

    def _tick_wind(self, delta: float, state: str, allow_face_tracking: bool) -> AvatarWindFrame:
        self.wind_time += delta
        self.wind_timer -= delta

        if self.wind_timer <= 0:
            if random.random() < wind_gust_chance:
                self.wind_target = _random_in(wind_gust_min, wind_gust_max)
                self.wind_timer = _random_in(0.9, 1.8)
            else:
                self.wind_target = _random_in(wind_base_min, wind_base_max)
                self.wind_timer = _random_in(wind_shift_min, wind_shift_max)

        smoothing = 1.0 - math.exp(-delta * wind_response)
        self.wind_current += (self.wind_target - self.wind_current) * smoothing

        t = self.wind_time
        strength_pulse = 1.0 + 0.16 * math.sin(t * 0.63 + 0.4) + 0.08 * math.sin(t * 1.37 + 1.9) + 0.04 * math.sin(t * 3.8 + 0.7)
        intensity = _clamp(self.wind_current * strength_pulse, 0.08, 0.95)

        dir_x = wind_base_dir_x + 0.08 * math.sin(t * 0.74 + 0.2) + 0.05 * math.sin(t * 2.15 + 2.1)
        dir_y = wind_base_dir_y + 0.03 * math.sin(t * 1.1 + 1.6) + 0.015 * math.sin(t * 4.4 + 0.5)
        dir_z = wind_base_dir_z + 0.07 * math.sin(t * 0.58 + 2.4) + 0.03 * math.sin(t * 1.83 + 1.1)
        dir_len = math.sqrt(dir_x * dir_x + dir_y * dir_y + dir_z * dir_z) or 1.0
        gravity = Vector3Frame(
            x=(dir_x / dir_len) * intensity,
            y=(dir_y / dir_len) * intensity,
            z=(dir_z / dir_len) * intensity,
        )

        head_x_target, head_y_target, neck_x_target, neck_y_target = self._head_targets(state, allow_face_tracking)
        self.head_current_x += (head_x_target - self.head_current_x) * head_lerp
        self.head_current_y += (head_y_target - self.head_current_y) * head_lerp
        self.neck_current_x += (neck_x_target - self.neck_current_x) * neck_lerp
        self.neck_current_y += (neck_y_target - self.neck_current_y) * neck_lerp

        extra_turn = wind_head_turn_y * intensity
        if state == "idle" and allow_face_tracking and self.face_present:
            extra_turn *= 0.22
        elif state == "listen":
            extra_turn *= 0.45
        elif state == "think":
            extra_turn = 0.0
        elif state == "speak":
            extra_turn *= 0.7

        head_tilt = wind_head_tilt_z * intensity
        neck_tilt = wind_neck_tilt_z * intensity
        if state == "think":
            head_tilt *= 0.35
            neck_tilt *= 0.35
        elif state == "idle" and allow_face_tracking and self.face_present:
            head_tilt *= 0.55
            neck_tilt *= 0.55

        return AvatarWindFrame(
            intensity=intensity,
            gravity=gravity,
            head=Vector3Frame(
                x=self.head_current_x,
                y=self.head_current_y + extra_turn,
                z=head_tilt,
            ),
            neck=Vector3Frame(
                x=self.neck_current_x,
                y=self.neck_current_y,
                z=neck_tilt,
            ),
        )

    def _head_targets(self, state: str, allow_face_tracking: bool) -> tuple[float, float, float, float]:
        if state == "think":
            sway = math.sin(self.wind_time * 0.9) * 0.018
            return -0.035, 0.05 + sway, -0.018, 0.02 + sway * 0.5

        if state in {"idle", "listen", "speak"} and allow_face_tracking and self.face_present:
            vertical = _clamp((self.face_norm_y - 0.5) * 2.0, -1.0, 1.0)
            horizontal = _clamp((0.5 - self.face_norm_x) * 2.0, -1.0, 1.0)
            if state == "idle":
                head_yaw_scale = 0.58
                head_pitch_scale = 0.52
                neck_yaw_scale = 0.55
                neck_pitch_scale = 0.48
                listen_bias = 0.006
            elif state == "listen":
                head_yaw_scale = 1.0
                head_pitch_scale = 0.9
                neck_yaw_scale = 1.0
                neck_pitch_scale = 0.9
                listen_bias = 0.012
            else:
                head_yaw_scale = 1.15
                head_pitch_scale = 1.05
                neck_yaw_scale = 1.1
                neck_pitch_scale = 1.0
                listen_bias = 0.0
            talk_nod = 0.0
            if state == "speak":
                talk_nod = math.sin(self.wind_time * 7.2) * 0.012

            head_x = _clamp(vertical * head_max_pitch * head_pitch_scale + listen_bias + talk_nod, -head_max_pitch, head_max_pitch)
            head_y = _clamp(horizontal * head_max_yaw * head_yaw_scale, -head_max_yaw, head_max_yaw)
            neck_x = _clamp(vertical * neck_max_pitch * neck_pitch_scale + listen_bias * 0.55 + talk_nod * 0.4, -neck_max_pitch, neck_max_pitch)
            neck_y = _clamp(horizontal * neck_max_yaw * neck_yaw_scale, -neck_max_yaw, neck_max_yaw)
            return head_x, head_y, neck_x, neck_y

        return 0.0, 0.0, 0.0, 0.0
