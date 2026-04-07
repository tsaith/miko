import { useEffect, useRef, useState } from 'react';
import './App.css';
import { EventsOn } from '../wailsjs/runtime/runtime';
import { GetRuntimeState, StartListening, StopListening } from '../wailsjs/go/main/App';
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
  config: {
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

  const chatContainerRef = useRef<HTMLDivElement>(null);
  const captionManagerRef = useRef<CaptionManager | null>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const sceneManagerRef = useRef<SceneManager | null>(null);
  const avatarManagerRef = useRef<AvatarManager | null>(null);

  useEffect(() => {
    GetRuntimeState()
      .then(setState)
      .catch((err) => setErrorText(String(err)));
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

        <div ref={chatContainerRef} className="chat-container">
          {chatHistory.length === 0 ? (
            <div className="empty-state">
            </div>
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
