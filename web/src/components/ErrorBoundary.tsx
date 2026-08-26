/**
 * ErrorBoundary — top-level crash shield. A render error anywhere in the
 * cockpit shows this fallback instead of a white screen (review BLOCKER-1).
 * The reload button re-renders the subtree; a persistent crash is caught
 * again by the same boundary.
 */
import { Component, type ErrorInfo, type ReactNode } from 'react';

interface ErrorBoundaryProps {
  readonly children: ReactNode;
}

interface ErrorBoundaryState {
  readonly error: Error | null;
}

export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null };

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // Zero-trace: log to console only (no egress).
    console.error('[cockpit] ErrorBoundary caught:', error.message, info.componentStack ?? '');
  }

  render(): ReactNode {
    const { error } = this.state;
    if (error !== null) {
      return (
        <div className="view-panel" role="alert" style={{ maxWidth: 640, margin: '48px auto', padding: 20 }}>
          <h2>Cockpit crashed</h2>
          <p className="fail ps-tabular" style={{ wordBreak: 'break-all' }}>
            {error.message || String(error)}
          </p>
          <button
            type="button"
            className="btn btn-primary"
            onClick={() => {
              this.setState({ error: null });
            }}
          >
            Reload view
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}