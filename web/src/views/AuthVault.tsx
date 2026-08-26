/**
 * AuthVault — 认证金库 (spec 01 §2 / spec 04 §1.1): Spotify PKCE 授权按钮
 * (跳转/新窗), YTM CDP 登录启动器, 代理配置, 凭据存活与过期倒计时
 * (<1h 橙色告警)。
 */
import { useState } from 'react';
import type { ApiClient } from '../api/client';
import type { AuthStatusResponse } from '../types/contracts';
import { formatCountdown, minutesUntil } from '../utils/format';
import { LockIcon, AlertTriangleIcon, RefreshCwIcon, CheckIcon } from '../components/icons';
import { useNow } from '../hooks/useNow';

export interface AuthVaultProps {
  readonly client: ApiClient;
  readonly status: AuthStatusResponse | null;
  readonly onRefresh: () => void;
}

export function AuthVault({ client, status, onRefresh }: AuthVaultProps) {
  const now = useNow(15_000);
  const [proxy, setProxy] = useState('');
  const [busyPlatform, setBusyPlatform] = useState<'spotify' | 'youtube_music' | null>(null);
  const [error, setError] = useState<string | null>(null);

  const sp = status?.spotify;
  const ytm = status?.youtubeMusic;

  const spExpiry = sp?.tokenExpiresAt ? formatCountdown(sp.tokenExpiresAt, now) : null;
  // Warning threshold: Spotify token validity below 60 minutes turns orange.
  const spExpiring = sp?.tokenExpiresAt !== undefined && minutesUntil(sp.tokenExpiresAt, now) < 60;

  async function runSpotifyAuthorize(): Promise<void> {
    setBusyPlatform('spotify');
    setError(null);
    try {
      const res = await client.getSpotifyAuthorizeURL();
      window.open(res.authorize_url, '_blank', 'noopener,noreferrer');
      onRefresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusyPlatform(null);
    }
  }

  async function runCDP(platform: 'youtube_music' | 'spotify'): Promise<void> {
    setBusyPlatform(platform);
    setError(null);
    try {
      await client.startCDPLogin({
        platform,
        headless: false,
        proxy: proxy.trim() || undefined,
      });
      // The login streams progress via SSE (TerminalDock); poll status later.
      window.setTimeout(() => void onRefresh(), 3000);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusyPlatform(null);
    }
  }

  return (
    <section className="view-panel auth-vault" data-testid="auth-vault" aria-label="Auth vault">
      <header className="panel-header">
        <h2>
          <LockIcon width={14} height={14} /> Auth Vault
        </h2>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onRefresh} disabled={busyPlatform !== null}>
          <RefreshCwIcon width={12} height={12} /> Refresh
        </button>
      </header>

      {error && <div className="vault-error">{error}</div>}

      <div className="vault-grid">
        {/* Spotify card */}
        <div className="vault-card vault-spotify">
          <div className="vault-card-head">
            <span className="platform-dot spotify" aria-hidden="true" />
            <strong>Spotify</strong>
            {sp?.authenticated ? (
              <span className="vault-ok">
                <CheckIcon width={11} height={11} /> authenticated
              </span>
            ) : (
              <span className="vault-missing">no credentials</span>
            )}
          </div>
          {sp?.authenticated && (
            <div className="vault-meta">
              <div>User: {sp.userDisplayName || '—'}</div>
              <div className={spExpiring ? 'expiry-warn' : ''}>
                Expires: {spExpiry ?? 'unknown'}
                {spExpiring && <AlertTriangleIcon width={11} height={11} />}
              </div>
            </div>
          )}
          <button
            type="button"
            className="btn btn-spotify"
            onClick={() => void runSpotifyAuthorize()}
            disabled={busyPlatform !== null}
          >
            {busyPlatform === 'spotify' ? 'Opening…' : 'Authorize (OAuth PKCE)'}
          </button>
          <p className="vault-hint">Opens Spotify consent in a new tab; the loopback callback completes the exchange.</p>
        </div>

        {/* YTM card */}
        <div className="vault-card vault-ytm">
          <div className="vault-card-head">
            <span className="platform-dot ytm" aria-hidden="true" />
            <strong>YouTube Music</strong>
            {ytm?.authenticated ? (
              <span className="vault-ok">
                <CheckIcon width={11} height={11} /> authenticated
              </span>
            ) : (
              <span className="vault-missing">no credentials</span>
            )}
          </div>
          {ytm?.authenticated && (
            <div className="vault-meta">
              <div>Account: {ytm.accountName || '—'}</div>
              <div>Auth type: {ytm.authType}</div>
            </div>
          )}
          <button
            type="button"
            className="btn btn-ytm"
            onClick={() => void runCDP('youtube_music')}
            disabled={busyPlatform !== null}
          >
            {busyPlatform === 'youtube_music' ? 'Launching CDP…' : 'CDP Login (browser)'}
          </button>
          <p className="vault-hint">Launches a Chromium session; progress streams to the terminal dock.</p>
        </div>
      </div>

      <div className="vault-proxy">
        <label htmlFor="vault-proxy">Proxy (HTTP/SOCKS5, optional)</label>
        <div className="vault-proxy-row">
          <input
            id="vault-proxy"
            type="text"
            value={proxy}
            onChange={(e) => setProxy(e.target.value)}
            placeholder="http://127.0.0.1:7890"
          />
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => void onRefresh()}>
            Apply
          </button>
        </div>
        <p className="vault-hint">Used by CDP login and YTM client (spec 01 §2); no restart required.</p>
      </div>
    </section>
  );
}