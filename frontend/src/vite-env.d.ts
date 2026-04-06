/// <reference types="vite/client" />

interface Window {
  go?: {
    main?: {
      App?: Record<string, (...args: any[]) => Promise<any>>;
    };
  };
  runtime?: {
    EventsOn?: (eventName: string, callback: (data: any) => void) => () => void;
  };
}
