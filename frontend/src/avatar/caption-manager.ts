const HIDE_HOLD_MS = 600;
const HIDE_MIN_MS = 400;

export class CaptionManager {
  private readonly onShow: (text: string) => void;
  private readonly onHide: () => void;
  private readonly onThinking: () => void;
  private pendingText = '';
  private shown = false;
  private hideTimer: ReturnType<typeof setTimeout> | null = null;

  constructor(
    onShow: (text: string) => void,
    onHide: () => void,
    onThinking: () => void = () => {},
  ) {
    this.onShow = onShow;
    this.onHide = onHide;
    this.onThinking = onThinking;
  }

  showThinking(): void {
    this.clearHide();
    this.shown = false;
    this.onThinking();
  }

  setPendingText(text: string): void {
    this.pendingText = text;
    this.shown = false;
  }

  showPendingNow(): void {
    if (!this.pendingText || this.shown) return;
    this.shown = true;
    this.clearHide();
    const text = this.pendingText;
    this.pendingText = '';
    this.onShow(text);
  }

  hideSoon(): void {
    this.clearHide();
    this.hideTimer = setTimeout(() => {
      this.onHide();
      this.hideTimer = null;
    }, Math.max(HIDE_MIN_MS, HIDE_HOLD_MS));
  }

  finalizeIdle(): void {
    if (this.pendingText && !this.shown) {
      this.showPendingNow();
      this.hideSoon();
      return;
    }
    if (!this.shown) {
      this.onHide();
    }
  }

  dispose(): void {
    this.clearHide();
    this.pendingText = '';
    this.shown = false;
  }

  private clearHide(): void {
    if (this.hideTimer !== null) {
      clearTimeout(this.hideTimer);
      this.hideTimer = null;
    }
  }
}
