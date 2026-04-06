import type { VRM } from '@pixiv/three-vrm';
import type { AvatarMotionFrame } from './motion-types';

export class ExpressionManager {
  private vrm: VRM | null = null;

  setVRM(vrm: VRM): void {
    this.vrm = vrm;
    this.applyMotion({ aa: 0, blink: 0 });
  }

  applyMotion(expressions: AvatarMotionFrame['expressions']): void {
    if (!this.vrm?.expressionManager) return;
    this.vrm.expressionManager.setValue('aa', clamp(expressions.aa));
    this.vrm.expressionManager.setValue('blink', clamp(expressions.blink));
  }

  dispose(): void {
    if (this.vrm?.expressionManager) {
      this.vrm.expressionManager.setValue('aa', 0);
      this.vrm.expressionManager.setValue('blink', 0);
    }
    this.vrm = null;
  }
}

function clamp(value: number): number {
  return Math.max(0, Math.min(1, value));
}
