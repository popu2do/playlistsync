/**
 * TerminalDock — SSE 实时日志流停靠栏 (spec 03 §4.1 [t] 折叠/展开, spec 01 §2).
 * 接收 LOG_STREAM / DIFF_PROGRESS / SYSTEM_SHUTDOWN 等事件并渲染为
 * 等宽日志行。纯内存、零持久化 (zero-trace)。
 */
import { useEffect, useRef } from 'react';
import { TerminalIcon, RefreshCwIcon, ChevronDownIcon } from './icons';
import type { LogStreamEvent } from '../types/contracts';

export interface TerminalLine {
  readonly id: number;
  readonly ts: string;
  readonly module: string;
  readonly level: string;
  readonly text: string;
}

export interface TerminalDockProps {
  readonly isOpen: boolean;
  readonly lines: readonly TerminalLine[];
  readonly onToggle: () => void;
  readonly onClear: () => void;
}

export function logToLine(id: number, log: LogStreamEvent): TerminalLine {
  return {
    id,
    ts: log.timestamp ?? '',
    module: log.module ?? 'sse',
    level: log.level ?? 'info',
    text: log.text ?? JSON.stringify(log),
  };
}

export function TerminalDock({ isOpen, lines, onToggle, onClear }: TerminalDockProps) {
  const bodyRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (isOpen && bodyRef.current) {
      bodyRef.current.scrollTop = bodyRef.current.scrollHeight;
    }
  }, [isOpen, lines.length]);

  return (
    <aside className={`terminal-dock ${isOpen ? 'is-open' : ''}`} data-testid="terminal-dock">
      <header className="terminal-header" onClick={onToggle} role="button" aria-expanded={isOpen}>
        <TerminalIcon width={14} height={14} />
        <span className="terminal-title">TERMINAL — LIVE SSE LOG</span>
        <span className="terminal-count ps-tabular">{lines.length}</span>
        <button
          type="button"
          className="terminal-clear"
          aria-label="Clear logs"
          onClick={(e) => {
            e.stopPropagation();
            onClear();
          }}
        >
          <RefreshCwIcon width={12} height={12} />
        </button>
        <ChevronDownIcon width={12} height={12} className={isOpen ? 'chev-open' : ''} />
      </header>
      {isOpen && (
        <div className="terminal-body ps-tabular" ref={bodyRef} data-testid="terminal-body">
          {lines.length === 0 ? (
            <div className="terminal-empty">Waiting for SSE events…</div>
          ) : (
            lines.map((l) => (
              <div key={l.id} className={`terminal-line level-${l.level}`}>
                <span className="t-ts">{l.ts}</span>
                <span className="t-module">[{l.module}]</span>
                <span className="t-text">{l.text}</span>
              </div>
            ))
          )}
        </div>
      )}
    </aside>
  );
}