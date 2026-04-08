import { useEffect, useRef, useState } from 'react';
import './App.css';
import { EventsOn } from '../wailsjs/runtime/runtime';
import { GetRuntimeState, SetRequireFaceToTalk, StartListening, StopListening } from '../wailsjs/go/main/App';
import { SceneManager } from './avatar/scene-manager';
import { AvatarManager } from './avatar/avatar-manager';
import { CaptionManager } from './avatar/caption-manager';
import type { AvatarMotionFrame } from './avatar/motion-types';

interface ChatMessage {
  role: 'user' | 'assistant';
  content: string;
}

interface ProviderStatus {
  openai_configured: boolean;
  deepgram_configured: boolean;
  cartesia_configured: boolean;
}

interface RuntimeState {
  app_name: string;
  config_path: string;
  log_path: string;
  log_directory: string;
  backend: {
    enabled: boolean;
    running: boolean;
    mode: string;
    transport: string;
    endpoint: string;
    service: string;
    version: string;
    status: string;
    last_error: string;
  };
  config: {
    app: {
      mode: 'development' | 'product';
      require_face_to_talk: boolean;
    };
    backend: {
      enabled: boolean;
      socket_path: string;
      launch_mode: string;
      python_module: string;
    };
    llm: {
      openai: {
        model: string;
      };
    };
    stt: {
      deepgram: {
        model: string;
        language: string;
      };
    };
    tts: {
      cartesia: {
        voice_id: string;
        model_id: string;
        sample_rate: number;
      };
    };
  };
  providers: ProviderStatus;
  platform_note: string;
}

function App() {
  const [state, setState] = useState<RuntimeState | null>(null);
  const [isListening, setIsListening] = useState(false);
  const [isSpeech, setIsSpeech] = useState(false);
  const [chatHistory, setChatHistory] = useState<ChatMessage[]>([]);
  const [chatStatus, setChatStatus] = useState<'idle' | 'thinking'>('idle');
  const [subtitle, setSubtitle] = useState('');
  const [subtitleThinking, setSubtitleThinking] = useState(false);
  const [subtitleVisible, setSubtitleVisible] = useState(false);
  const [errorText, setErrorText] = useState('');
  const [menuOpen, setMenuOpen] = useState(false);

  const chatContainerRef = useRef<HTMLDivElement>(null);
  const captionManagerRef = useRef<CaptionManager | null>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const sceneManagerRef = useRef<SceneManager | null>(null);
  const avatarManagerRef = useRef<AvatarManager | null>(null);
  const autoStartTriggeredRef = useRef(false);

  useEffect(() => {
    let cancelled = false;

    const refreshState = async () => {
      try {
        const nextState = await GetRuntimeState();
        if (!cancelled) {
          setState(nextState as unknown as RuntimeState);
        }
      } catch (err) {
        if (!cancelled) {
          setErrorText(String(err));
        }
      }
    };

    void refreshState();
    const timer = window.setInterval(() => {
      void refreshState();
    }, 5000);

    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, []);

  useEffect(() => {
    if (!canvasRef.current) return;

    const sceneManager = new SceneManager(canvasRef.current);
    sceneManagerRef.current = sceneManager;

    const avatarManager = new AvatarManager();
    avatarManagerRef.current = avatarManager;

    sceneManager.setAvatar(avatarManager);
    avatarManager
      .load(sceneManager.threeScene, '/vrm/Yuna.vrm')
      .then((headPos) => {
        sceneManager.frameCamera(headPos);
      })
      .catch((err) => setErrorText(`VRM 載入失敗：${String(err)}`));

    return () => {
      sceneManager.dispose();
      avatarManager.dispose();
      sceneManagerRef.current = null;
      avatarManagerRef.current = null;
    };
  }, []);

  useEffect(() => {
    captionManagerRef.current = new CaptionManager(
      (text) => {
        setSubtitle(text);
        setSubtitleThinking(false);
        setSubtitleVisible(true);
      },
      () => {
        setSubtitleVisible(false);
        setSubtitleThinking(false);
      },
      () => {
        setSubtitleThinking(true);
        setSubtitleVisible(true);
      },
    );

    return () => {
      captionManagerRef.current?.dispose();
      captionManagerRef.current = null;
    };
  }, []);

  useEffect(() => {
    const offListening = EventsOn('listening_state', (data: { is_listening: boolean }) => {
      setIsListening(Boolean(data?.is_listening));
      if (!data?.is_listening) setIsSpeech(false);
    });
    const offVad = EventsOn('vad_status', (data: { is_speech: boolean }) => {
      setIsSpeech(Boolean(data?.is_speech));
    });
    const offChatUpdate = EventsOn('chat_update', (data: ChatMessage) => {
      if (!data?.content) return;
      setChatHistory((prev) => [...prev, data]);
      if (data.role === 'assistant') {
        captionManagerRef.current?.setPendingText(data.content);
      }
    });
    const offChatStatus = EventsOn('chat_status', (data: { status: 'idle' | 'thinking' }) => {
      setChatStatus(data?.status ?? 'idle');
      if (data?.status === 'thinking') {
        captionManagerRef.current?.showThinking();
      } else if (data?.status === 'idle') {
        captionManagerRef.current?.finalizeIdle();
      }
    });
    const offMotion = EventsOn('avatar_motion', (data: AvatarMotionFrame) => {
      avatarManagerRef.current?.applyMotion(data);
    });
    const offTtsStart = EventsOn('tts_start', () => {
      captionManagerRef.current?.showPendingNow();
    });
    const offTtsEnd = EventsOn('tts_end', () => {
      captionManagerRef.current?.hideSoon();
    });
    const offAppError = EventsOn('app_error', (data: { message?: string }) => {
      if (data?.message) {
        setErrorText(data.message);
      }
    });

    return () => {
      offListening();
      offVad();
      offChatUpdate();
      offChatStatus();
      offMotion();
      offTtsStart();
      offTtsEnd();
      offAppError();
    };
  }, []);

  useEffect(() => {
    const container = chatContainerRef.current;
    if (!container) return;
    container.scrollTo({
      top: container.scrollHeight,
      behavior: 'smooth',
    });
  }, [chatHistory, chatStatus]);

  const providersReady = Boolean(
    state?.providers?.openai_configured &&
      state?.providers?.deepgram_configured &&
      state?.providers?.cartesia_configured,
  );
  const isProductMode = (state?.config?.app?.mode ?? 'product') === 'product';
  const requireFaceToTalk = Boolean(state?.config?.app?.require_face_to_talk);
  const backendEnabled = Boolean(state?.backend?.enabled);
  const backendReady = Boolean(state?.backend?.enabled && state?.backend?.running);
  const backendTransport = state?.backend?.transport || 'unix';
  const backendEndpoint = state?.backend?.endpoint || 'n/a';
  const backendLabel = !backendEnabled
    ? 'Sidecar Disabled'
    : backendReady
      ? `Sidecar ${state?.backend?.status || 'Ready'}`
      : 'Sidecar Offline';
  const backendDetail = backendReady
    ? [state?.backend?.service, state?.backend?.version, backendTransport].filter(Boolean).join(' • ')
    : state?.backend?.last_error || `${backendTransport} • ${backendEndpoint}`;

  useEffect(() => {
    if (!isProductMode || autoStartTriggeredRef.current || isListening || !providersReady) {
      return;
    }
    autoStartTriggeredRef.current = true;
    void StartListening().catch((err) => {
      setErrorText(String(err));
      autoStartTriggeredRef.current = false;
    });
  }, [isProductMode, isListening, providersReady]);

  const toggleListening = async () => {
    setErrorText('');
    try {
      if (isListening) {
        await StopListening();
      } else {
        await StartListening();
      }
    } catch (err) {
      setErrorText(String(err));
    }
  };

  const handleProductCanvasClick = () => {
    if (!isProductMode) {
      return;
    }
    setMenuOpen(true);
  };

  const setRequireFaceToTalkMode = async (enabled: boolean) => {
    try {
      const next = await SetRequireFaceToTalk(enabled);
      setState(next as unknown as RuntimeState);
    } catch (err) {
      setErrorText(String(err));
    }
  };

  const renderProductMenu = () => (
    <div className="product-menu-backdrop" onClick={() => setMenuOpen(false)}>
      <div className="product-menu" onClick={(event) => event.stopPropagation()}>
        <div className="product-menu-title">主選單</div>
        <button
          className={`product-menu-item ${isListening ? 'danger' : 'primary'}`}
          onClick={async () => {
            await toggleListening();
          }}
          disabled={!providersReady && !isListening}
        >
          {isListening ? '停止交談' : '開始交談'}
        </button>
        <div className="product-menu-group">
          <div className="product-menu-group-title">交談模式</div>
          <button
            className={`product-menu-item option ${requireFaceToTalk ? 'selected' : ''}`}
            onClick={() => setRequireFaceToTalkMode(true)}
          >
            看到使用者才進行交談
          </button>
          <button
            className={`product-menu-item option ${!requireFaceToTalk ? 'selected' : ''}`}
            onClick={() => setRequireFaceToTalkMode(false)}
          >
            不需要看到使用者也能交談
          </button>
        </div>
        <button className="product-menu-item secondary" onClick={() => setMenuOpen(false)}>
          離開菜單
        </button>
        {errorText ? <div className="product-menu-error">{errorText}</div> : null}
      </div>
    </div>
  );

  if (isProductMode) {
    return (
      <div className="product-shell" onClick={handleProductCanvasClick}>
        <section className="avatar-panel product">
          <canvas ref={canvasRef} />
          <div className={`status-orb ${isSpeech ? 'active' : ''}`} />
          <div className={`subtitle-box${subtitleVisible ? ' visible' : ''}`}>
            {subtitleThinking ? (
              <span className="subtitle-thinking">
                思考中
                <span className="caption-dots">
                  <span /><span /><span />
                </span>
              </span>
            ) : subtitle}
          </div>
        </section>
        {menuOpen ? renderProductMenu() : null}
      </div>
    );
  }

  return (
    <div className="app-shell">
      <section className="avatar-panel">
        <canvas ref={canvasRef} />
        <div className={`status-orb ${isSpeech ? 'active' : ''}`} />

        <div className={`subtitle-box${subtitleVisible ? ' visible' : ''}`}>
          {subtitleThinking ? (
            <span className="subtitle-thinking">
              思考中
              <span className="caption-dots">
                <span /><span /><span />
              </span>
            </span>
          ) : subtitle}
        </div>
      </section>

      <aside className="chat-panel">
        <div className="panel-status">
          <div className={`pill ${providersReady ? 'ready' : 'warning'}`}>
            {providersReady ? 'API Ready' : 'API Missing'}
          </div>
        </div>

        <div className={`backend-status ${backendReady ? 'ready' : backendEnabled ? 'warning' : 'idle'}`}>
          <div className="backend-status-label">{backendLabel}</div>
          <div className="backend-status-detail">{backendDetail}</div>
        </div>

        <div ref={chatContainerRef} className="chat-container">
          {chatHistory.length === 0 ? (
            <div className="empty-state" />
          ) : (
            chatHistory.map((msg, idx) => (
              <div key={`${msg.role}-${idx}`} className={`chat-bubble ${msg.role}`}>
                {msg.content}
              </div>
            ))
          )}

          {chatStatus === 'thinking' && (
            <div className="chat-bubble assistant thinking">
              <span className="dot" />
              <span className="dot" />
              <span className="dot" />
            </div>
          )}
        </div>

        {errorText ? <div className="error-banner">{errorText}</div> : null}

        <button
          className={`mic-btn ${isListening ? 'listening' : ''}`}
          onClick={toggleListening}
          disabled={!providersReady && !isListening}
        >
          {isListening ? '停止交談' : '開始交談'}
        </button>
      </aside>
    </div>
  );
}

export default App;
