import type { VRM } from '@pixiv/three-vrm';
import type { AvatarMotionFrame } from './motion-types';

export class ExpressionManager {
  private vrm: VRM | null = null;

  setVRM(vrm: VRM): void {
    this.vrm = vrm;
    this.applyMotion({ aa: 0, blink: 0 });
  }

  applyMotion(expressions: AvatarMotionFrame['expressions']): void {
    const manager = this.vrm?.expressionManager;
    if (!manager) return;

    const mouthValue = clamp(expressions.aa * 1.35);
    manager.setValue('aa', mouthValue);
    manager.setValue('blink', clamp(expressions.blink));
  }

  dispose(): void {
    const manager = this.vrm?.expressionManager;
    if (manager) {
      manager.setValue('aa', 0);
      manager.setValue('blink', 0);
    }
    this.vrm = null;
  }
}

function clamp(value: number): number {
  return Math.max(0, Math.min(1, value));
}
