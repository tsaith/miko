import * as THREE from 'three';
import type { AvatarManager } from './avatar-manager';

export class SceneManager {
  private renderer: THREE.WebGLRenderer;
  private scene: THREE.Scene;
  private camera: THREE.PerspectiveCamera;
  private avatarManager: AvatarManager | null = null;
  private lastTime = 0;
  private rafId = 0;
  private resizeObserver: ResizeObserver;

  constructor(canvas: HTMLCanvasElement) {
    this.renderer = new THREE.WebGLRenderer({ canvas, antialias: true, alpha: true });
    this.renderer.setPixelRatio(window.devicePixelRatio);
    this.renderer.outputColorSpace = THREE.SRGBColorSpace;
    this.renderer.toneMapping = THREE.ACESFilmicToneMapping;
    this.renderer.toneMappingExposure = 1.05;

    this.scene = new THREE.Scene();
    this.scene.fog = new THREE.FogExp2('#09131b', 0.5);

    const width = canvas.clientWidth || canvas.width;
    const height = canvas.clientHeight || canvas.height;
    this.camera = new THREE.PerspectiveCamera(28, width / height, 0.01, 20);
    this.camera.position.set(0, 1.56, 0.85);
    this.camera.lookAt(0, 1.4, 0);

    const ambient = new THREE.AmbientLight(0xffffff, 1.35);
    this.scene.add(ambient);

    const keyLight = new THREE.DirectionalLight('#fff3d1', 1.6);
    keyLight.position.set(1.4, 2.4, 2.2);
    this.scene.add(keyLight);

    const fillLight = new THREE.DirectionalLight('#84ccff', 0.8);
    fillLight.position.set(-1.4, 1.2, 1.2);
    this.scene.add(fillLight);

    this.resizeObserver = new ResizeObserver(() => this.onResize());
    this.resizeObserver.observe(canvas);
    this.onResize();

    this.lastTime = performance.now();
    this.animate();
  }

  get threeScene(): THREE.Scene {
    return this.scene;
  }

  setAvatar(manager: AvatarManager): void {
    this.avatarManager = manager;
  }

  frameCamera(headPosition: THREE.Vector3): void {
    const lookTarget = new THREE.Vector3(0, headPosition.y - 0.04, 0);
    this.camera.position.set(0.02, headPosition.y + 0.04, 0.9);
    this.camera.lookAt(lookTarget);
    this.camera.updateProjectionMatrix();
  }

  private onResize(): void {
    const canvas = this.renderer.domElement;
    const width = canvas.clientWidth;
    const height = canvas.clientHeight;
    if (width === 0 || height === 0) return;
    this.renderer.setSize(width, height, false);
    this.camera.aspect = width / height;
    this.camera.updateProjectionMatrix();
  }

  private animate(): void {
    this.rafId = requestAnimationFrame(() => this.animate());

    const now = performance.now();
    const delta = Math.min((now - this.lastTime) / 1000, 0.1);
    this.lastTime = now;

    this.avatarManager?.tick(delta);
    this.renderer.render(this.scene, this.camera);
  }

  dispose(): void {
    cancelAnimationFrame(this.rafId);
    this.resizeObserver.disconnect();
    this.renderer.dispose();
    this.avatarManager = null;
  }
}
