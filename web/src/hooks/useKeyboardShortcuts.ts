/**
 * Full-keyboard cockpit navigation (spec 01 §2 / spec 03 §5).
 *
 * Global shortcuts when NOT typing in an input/textarea:
 *   j / ↓        move focus down (diff list)
 *   k / ↑        move focus up (diff list)
 *   1..9         select candidate N in the arbitration drawer
 *   s            skip the focused track
 *   c            open custom Target ID input
 *   a            trigger Apply (when invariants pass)
 *   Enter        open arbitration / confirm custom id
 *   Esc          close modal / dismiss help
 *   /            focus the help cheat-sheet toggle
 *   t            toggle the terminal dock
 */
import { useEffect } from 'react';

export interface KeyboardShortcutHandlers {
  /** Move the row focus by a delta (-1 / +1). */
  moveFocus: (delta: number) => void;
  /** Select a candidate by its 1-based index inside the drawer (1..9). */
  selectCandidate: (candidateIndex: number) => void;
  /** Skip the focused track. */
  skip: () => void;
  /** Open the custom target-id input. */
  openCustom: () => void;
  /** Trigger apply (if invariants pass). */
  apply: () => void;
  /** Open/confirm the arbitration drawer for the focused row. */
  openArbitration: () => void;
  /** Escape handler: dismiss the top modal. */
  onEscape: () => void;
  /** Toggle the terminal dock. */
  toggleTerminal: () => void;
  /** Open/close the help cheat sheet. */
  toggleHelp: () => void;
}

export function useKeyboardShortcuts(handlers: KeyboardShortcutHandlers): void {
  useEffect(() => {
    function onKeyDown(e: KeyboardEvent): void {
      const active = document.activeElement;
      const tag = active ? active.tagName : '';
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') {
        if (e.key === 'Escape') {
          if (active instanceof HTMLElement) active.blur();
          handlers.onEscape();
        }
        return;
      }

      switch (e.key) {
        case 'j':
        case 'ArrowDown':
          e.preventDefault();
          handlers.moveFocus(1);
          break;
        case 'k':
        case 'ArrowUp':
          e.preventDefault();
          handlers.moveFocus(-1);
          break;
        case 'Enter':
          if (e.ctrlKey || e.metaKey) {
            e.preventDefault();
            handlers.apply();
          } else {
            e.preventDefault();
            handlers.openArbitration();
          }
          break;
        case 'a':
          e.preventDefault();
          handlers.apply();
          break;
        case 's':
          e.preventDefault();
          handlers.skip();
          break;
        case 'c':
          e.preventDefault();
          handlers.openCustom();
          break;
        case 't':
          e.preventDefault();
          handlers.toggleTerminal();
          break;
        case '?':
          e.preventDefault();
          handlers.toggleHelp();
          break;
        case 'Escape':
          e.preventDefault();
          handlers.onEscape();
          break;
        default: {
          if (/^[1-9]$/.test(e.key)) {
            e.preventDefault();
            handlers.selectCandidate(Number(e.key));
          }
          break;
        }
      }
    }
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
    // Handlers are recreated each render; re-binding is cheap and correct.
  }, [handlers]);
}