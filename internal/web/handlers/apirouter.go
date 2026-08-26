package handlers

import (
	"net/http"

	"playlistsync/internal/invariants"
)

// RegisterAll mounts every cockpit endpoint on the given mux. cmd/web calls
// this once with the production HandlerConfig; tests can mount individual
// domains via their Register* functions.
func RegisterAll(mux *http.ServeMux, v invariants.InvariantVerifier, cfg HandlerConfig) {
	RegisterSessionHandlers(mux, cfg)
	RegisterAuthHandlers(mux, cfg)
	RegisterReconcileHandlers(mux, cfg)
	RegisterInspectHandlers(mux, cfg)
	RegisterVerifyHandlers(mux, v, cfg)
	RegisterReportHandlers(mux, cfg)
	RegisterEventsHandler(mux, cfg)
}
