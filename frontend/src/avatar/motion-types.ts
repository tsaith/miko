export interface Vector3Frame {
  x: number;
  y: number;
  z: number;
}

export interface AvatarMotionFrame {
  version: number;
  sequence: number;
  pose: {
    base: {
      leftUpperArmZ: number;
      rightUpperArmZ: number;
    };
    breathing: {
      value: number;
      shoulderZ: number;
      chestZ: number;
    };
    gaze: {
      eyeY: number;
    };
    weightShift: {
      hipsZ: number;
      spineZ: number;
    };
    wind: {
      intensity: number;
      gravity: Vector3Frame;
      head: Vector3Frame;
      neck: Vector3Frame;
    };
  };
  expressions: {
    aa: number;
    blink: number;
  };
}
