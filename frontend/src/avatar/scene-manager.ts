import * as THREE from 'three';
import type { AvatarManager } from './avatar-manager';

export class SceneManager {
  private renderer: THREE.WebGLRenderer;
  private scene: THREE.Scene;
  private camera: THREE.PerspectiveCamera;
  private spotLight: THREE.SpotLight;
  private spotTarget: THREE.Object3D;
  private avatarManager: AvatarManager | null = null;
  private lastTime = 0;
  private rafId = 0;
  private resizeObserver: ResizeObserver;

  constructor(canvas: HTMLCanvasElement) {
    this.renderer = new THREE.WebGLRenderer({ canvas, antialias: true, alpha: false });
    this.renderer.setPixelRatio(window.devicePixelRatio);
    this.renderer.outputColorSpace = THREE.SRGBColorSpace;
    this.renderer.toneMapping = THREE.ACESFilmicToneMapping;
    this.renderer.toneMappingExposure = 0.9;

    this.scene = new THREE.Scene();
    this.scene.background = new THREE.Color('#000000');
    //this.scene.fog = new THREE.FogExp2('#0f1a23', 0.24);

    const width = canvas.clientWidth || canvas.width;
    const height = canvas.clientHeight || canvas.height;
    this.camera = new THREE.PerspectiveCamera(28, width / height, 0.01, 20);

    this.camera.position.set(0, 1.56, 0.85);
    this.camera.lookAt(0, 1.4, 1.0);

    const ambient = new THREE.AmbientLight('#fff4ea', 0.5);
    this.scene.add(ambient);

    const hemiLight = new THREE.HemisphereLight('#cfe8ff', '#081018', 1.8);
    hemiLight.position.set(0, 2.2, 0);
    //this.scene.add(hemiLight);

    const keyLight = new THREE.DirectionalLight('#ffe7c7', 0.6);
    keyLight.position.set(1.35, 2.2, 1.7);
    this.scene.add(keyLight);

    const fillLight = new THREE.DirectionalLight('#a7d8ff', 0.85);
    fillLight.position.set(-1.7, 1.3, 1.15);
    this.scene.add(fillLight);

    const rimLight = new THREE.DirectionalLight('#ffd5cf', 1.35);
    rimLight.position.set(-0.95, 2.0, -2.5);
    this.scene.add(rimLight);

    this.spotLight = new THREE.SpotLight('#fff6e2', 1.5, 9, Math.PI / 5.2, 0.5, 1.45);
    this.spotLight.position.set(0.48, 2.35, 1.55);
    this.spotLight.castShadow = false;

    this.spotTarget = new THREE.Object3D();
    this.spotTarget.position.set(0, 1.42, 0);
    this.scene.add(this.spotTarget);

    this.spotLight.target = this.spotTarget;
    this.scene.add(this.spotLight);

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
    const lookTarget = new THREE.Vector3(0, headPosition.y + 0.04, 0);
    this.camera.position.set(0.0, headPosition.y - 0.08, headPosition.z + 0.9);
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
