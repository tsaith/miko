import type { VRM } from '@pixiv/three-vrm';
import type { AvatarMotionFrame } from './motion-types';
import * as THREE from 'three';

interface WindJoint {
  boneName: string;
  gravityDir: { x: number; y: number; z: number };
  settings: { gravityPower: number };
  original: { x: number; y: number; z: number };
  originalPower: number;
  isHair: boolean;
}

interface BoneRotationState {
  bone: THREE.Object3D;
  rotation: THREE.Euler;
}

export class PoseManager {
  private vrm: VRM | null = null;
  private windJoints: WindJoint[] = [];
  private baseRotations = new Map<string, BoneRotationState>();
  private readonly hairWindPower = 0.7;

  setVRM(vrm: VRM): void {
    this.vrm = vrm;
    this.windJoints = [];
    this.baseRotations.clear();
    vrm.springBoneManager?.joints.forEach((joint) => {
      const dir = joint.settings.gravityDir;
      const boneName = joint.bone.name ?? '';
      this.windJoints.push({
        boneName,
        gravityDir: dir,
        settings: joint.settings as { gravityPower: number },
        original: { x: dir.x, y: dir.y, z: dir.z },
        originalPower: joint.settings.gravityPower,
        isHair: /hair/i.test(boneName),
      });
    });

    this.captureBaseRotation('leftShoulder');
    this.captureBaseRotation('rightShoulder');
    this.captureBaseRotation('leftUpperArm');
    this.captureBaseRotation('rightUpperArm');
    this.captureBaseRotation('chest');
    this.captureBaseRotation('upperChest');
    this.captureBaseRotation('leftEye');
    this.captureBaseRotation('rightEye');
    this.captureBaseRotation('hips');
    this.captureBaseRotation('spine');
    this.captureBaseRotation('head');
    this.captureBaseRotation('neck');

    this.applyMotion({
      base: {
        leftUpperArmZ: -Math.PI * 0.3,
        rightUpperArmZ: Math.PI * 0.3,
      },
      breathing: {
        value: 0,
        shoulderZ: 0,
        chestZ: 0,
      },
      gaze: {
        eyeX: 0,
        eyeY: 0,
      },
      weightShift: {
        hipsZ: 0,
        spineZ: 0,
      },
      wind: {
        intensity: 0,
        gravity: { x: 0, y: 0, z: 0 },
        head: { x: 0, y: 0, z: 0 },
        neck: { x: 0, y: 0, z: 0 },
      },
    });
  }

  applyMotion(pose: AvatarMotionFrame['pose']): void {
    if (!this.vrm) return;
    const { humanoid } = this.vrm;

    const leftShoulder = humanoid.getNormalizedBoneNode('leftShoulder');
    const rightShoulder = humanoid.getNormalizedBoneNode('rightShoulder');
    const leftUpperArm = humanoid.getNormalizedBoneNode('leftUpperArm');
    const rightUpperArm = humanoid.getNormalizedBoneNode('rightUpperArm');

    if (leftShoulder && rightShoulder) {
      this.applyBoneRotation('leftShoulder', { z: pose.breathing.shoulderZ });
      this.applyBoneRotation('rightShoulder', { z: -pose.breathing.shoulderZ });
      this.applyBoneRotation('leftUpperArm', { z: pose.base.leftUpperArmZ });
      this.applyBoneRotation('rightUpperArm', { z: pose.base.rightUpperArmZ });
    } else {
      this.applyBoneRotation('leftUpperArm', { z: pose.base.leftUpperArmZ + pose.breathing.shoulderZ });
      this.applyBoneRotation('rightUpperArm', { z: pose.base.rightUpperArmZ - pose.breathing.shoulderZ });
    }

    this.applyBoneRotation('chest', { z: pose.breathing.chestZ });
    this.applyBoneRotation('upperChest', { z: -pose.breathing.chestZ });

    this.applyBoneRotation('leftEye', { x: pose.gaze.eyeX, y: pose.gaze.eyeY });
    this.applyBoneRotation('rightEye', { x: pose.gaze.eyeX, y: pose.gaze.eyeY });

    this.applyBoneRotation('hips', { z: pose.weightShift.hipsZ });
    this.applyBoneRotation('spine', { z: pose.weightShift.spineZ });

    for (const joint of this.windJoints) {
      const nextX = joint.original.x + pose.wind.gravity.x;
      const nextY = joint.original.y + pose.wind.gravity.y;
      const nextZ = joint.original.z + pose.wind.gravity.z;
      const nextDir = new THREE.Vector3(nextX, nextY, nextZ).normalize();

      joint.gravityDir.x = nextDir.x;
      joint.gravityDir.y = nextDir.y;
      joint.gravityDir.z = nextDir.z;

      joint.settings.gravityPower = joint.isHair
        ? joint.originalPower + pose.wind.intensity * this.hairWindPower
        : joint.originalPower;
    }

    this.applyBoneRotation('head', { x: pose.wind.head.x, y: pose.wind.head.y, z: pose.wind.head.z });
    this.applyBoneRotation('neck', { x: pose.wind.neck.x, y: pose.wind.neck.y, z: pose.wind.neck.z });
  }

  dispose(): void {
    for (const joint of this.windJoints) {
      joint.gravityDir.x = joint.original.x;
      joint.gravityDir.y = joint.original.y;
      joint.gravityDir.z = joint.original.z;
      joint.settings.gravityPower = joint.originalPower;
    }
    this.windJoints = [];
    this.baseRotations.clear();
    this.vrm = null;
  }

  private captureBaseRotation(boneName: Parameters<VRM['humanoid']['getNormalizedBoneNode']>[0]): void {
    if (!this.vrm) return;
    const bone = this.vrm.humanoid.getNormalizedBoneNode(boneName);
    if (!bone) return;
    this.baseRotations.set(boneName, {
      bone,
      rotation: bone.rotation.clone(),
    });
  }

  private applyBoneRotation(
    boneName: string,
    offset: Partial<Record<'x' | 'y' | 'z', number>>,
  ): void {
    const state = this.baseRotations.get(boneName);
    if (!state) return;
    state.bone.rotation.set(
      state.rotation.x + (offset.x ?? 0),
      state.rotation.y + (offset.y ?? 0),
      state.rotation.z + (offset.z ?? 0),
      state.rotation.order,
    );
  }
}
