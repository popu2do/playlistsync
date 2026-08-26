/**
 * Idle-watchdog heartbeat: POST /api/v1/session/heartbeat every 30s while the
 * cockpit is open (spec 02 §3.3: REST requests + frontend heartbeat reset the
 * 15-minute idle watchdog).
 */
import { useEffect, useRef } from 'react';
import type { ApiClient } from '../api/client';

export function useHeartbeat(client: ApiClient | null, enabled = true): void {
  const clientRef = useRef(client);
  clientRef.current = client;

  useEffect(() => {
    if (!enabled) return undefined;
    let cancelled = false;

    const beat = async (): Promise<void> => {
      const c = clientRef.current;
      if (!c || cancelled) return;
      try {
        await c.heartbeat();
      } catch {
        // Silent: a failed heartbeat must not crash the cockpit.
      }
    };

    void beat();
    const timer = window.setInterval(() => void beat(), 30_000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [enabled]);
}