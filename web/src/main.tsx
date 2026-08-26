/**
 * Entry point — mounts the cockpit SPA. No external assets, zero CDN.
 * Wrapped in ErrorBoundary so a render error shows a recovery panel
 * instead of a white screen (review BLOCKER-1).
 */
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import { ErrorBoundary } from './components/ErrorBoundary';

const rootEl = document.getElementById('root');
if (!rootEl) {
  throw new Error('[cockpit] #root element missing from index.html');
}

createRoot(rootEl).render(
  <StrictMode>
    <ErrorBoundary>
      <App />
    </ErrorBoundary>
  </StrictMode>,
);