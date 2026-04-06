import * as THREE from 'three';
import { GLTFLoader } from 'three/examples/jsm/loaders/GLTFLoader.js';
import { VRM, VRMLoaderPlugin, VRMUtils } from '@pixiv/three-vrm';
import { ExpressionManager } from './expression-manager';
import { PoseManager } from './pose-manager';
import type { AvatarMotionFrame } from './motion-types';

export class AvatarManager {
  private vrm: VRM | null = null;
  private scene: THREE.Scene | null = null;
  private lastMotion: AvatarMotionFrame | null = null;
  readonly expressions = new ExpressionManager();
  readonly pose = new PoseManager();

  async load(scene: THREE.Scene, url: string): Promise<THREE.Vector3> {
    const loader = new GLTFLoader();
    loader.register((parser: any) => new VRMLoaderPlugin(parser));

    const gltf = await loader.loadAsync(url);
    const vrm = gltf.userData.vrm as VRM | undefined;
    if (!vrm) throw new Error(`No VRM data found in ${url}`);

    if (vrm.meta.metaVersion === '0') {
      VRMUtils.rotateVRM0(vrm);
    }

    scene.add(vrm.scene);
    this.vrm = vrm;
    this.scene = scene;
    this.expressions.setVRM(vrm);
    this.pose.setVRM(vrm);
    if (this.lastMotion) this.applyMotion(this.lastMotion);

    vrm.update(0);
    scene.updateMatrixWorld(true);
    return this.centerBustView();
  }

  applyMotion(frame: AvatarMotionFrame): void {
    this.lastMotion = frame;
    this.expressions.applyMotion(frame.expressions);
    this.pose.applyMotion(frame.pose);
  }

  tick(delta: number): void {
    this.vrm?.update(delta);
  }

  dispose(): void {
    this.expressions.dispose();
    this.pose.dispose();
    if (this.vrm) {
      this.scene?.remove(this.vrm.scene);
      VRMUtils.deepDispose(this.vrm.scene);
    }
    this.vrm = null;
    this.scene = null;
    this.lastMotion = null;
  }

  private centerBustView(): THREE.Vector3 {
    if (!this.vrm || !this.scene) {
      return new THREE.Vector3(0, 1.45, 0);
    }

    const headPos = this.getHeadWorldPosition();
    const targetHead = new THREE.Vector3(0, 1.46, 0);
    const offset = targetHead.sub(headPos);
    this.vrm.scene.position.add(offset);
    this.scene.updateMatrixWorld(true);
    return this.getHeadWorldPosition();
  }

  private getHeadWorldPosition(): THREE.Vector3 {
    if (!this.vrm) {
      return new THREE.Vector3(0, 1.45, 0);
    }

    const headBone = this.vrm.humanoid.getNormalizedBoneNode('head');
    const headPos = new THREE.Vector3();
    if (headBone) {
      headBone.getWorldPosition(headPos);
      return headPos;
    }
    return headPos.set(0, 1.45, 0);
  }
}
