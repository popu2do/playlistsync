package handlers

import (
	"context"
	"net/http"
	"time"
)

// RegisterSessionHandlers mounts the session & system-management endpoints
// (spec 02 §2.1, spec 07 §3.1):
//
//	GET  /api/v1/session            session status + invariants summary
//	POST /api/v1/session/heartbeat  refresh the idle watchdog
//	POST /api/v1/session/shutdown   broadcast SYSTEM_SHUTDOWN + graceful shutdown
func RegisterSessionHandlers(mux *http.ServeMux, cfg HandlerConfig) {
	mux.HandleFunc("GET /api/v1/session", sessionStatus(cfg))
	mux.HandleFunc("POST /api/v1/session/heartbeat", sessionHeartbeat(cfg))
	mux.HandleFunc("POST /api/v1/session/shutdown", sessionShutdown(cfg))
}

// sessionStatusResponse is the GET /session body (spec 07 §3.1).
type sessionStatusResponse struct {
	SessionID     string               `json:"session_id"`
	Status        string               `json:"status"`
	Port          int                  `json:"port"`
	ClientCount   int                  `json:"client_count"`
	StartedAt     time.Time            `json:"started_at"`
	LastHeartbeat time.Time            `json:"last_heartbeat_at"`
	Invariants    invariantsSummaryDTO `json:"invariants_summary"`
}

// invariantsSummaryDTO is the five-flag invariant radar summary (spec 07 §3.1).
type invariantsSummaryDTO struct {
	CountConserved  bool `json:"count_conserved"`
	UniquenessValid bool `json:"uniqueness_valid"`
	OrderMonotonic  bool `json:"order_lis_monotonic"`
	DiffComplete    bool `json:"diff_complete"`
	ZeroTraceClean  bool `json:"zero_trace_clean"`
}

func sessionStatus(cfg HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		port := 0
		if cfg.Port != nil {
			port = cfg.Port()
		}
		clients := 0
		if cfg.ClientCount != nil {
			clients = cfg.ClientCount()
		}
		// status mirrors the reconcile job lifecycle (IDLE unless a job is
		// running or has completed/failed since the last session read).
		status := "IDLE"
		lastHeartbeat := time.Time{}
		if cfg.State != nil {
			_, jobStatus, _, _ := cfg.State.Snapshot()
			switch jobStatus {
			case "running":
				status = "RUNNING"
			case "complete", "failed":
				status = "READY"
			}
			lastHeartbeat = cfg.State.lastHeartbeat()
		}

		var summary invariantsSummaryDTO
		if cfg.State != nil {
			count, unique, order, diff, trace := cfg.State.InvariantsSummary()
			summary = invariantsSummaryDTO{
				CountConserved:  count,
				UniquenessValid: unique,
				OrderMonotonic:  order,
				DiffComplete:    diff,
				ZeroTraceClean:  trace,
			}
		}

		writeJSON(w, http.StatusOK, sessionStatusResponse{
			SessionID:     cfg.SessionID,
			Status:        status,
			Port:          port,
			ClientCount:   clients,
			StartedAt:     cfg.StartTime,
			LastHeartbeat: lastHeartbeat,
			Invariants:    summary,
		})
	}
}

func sessionHeartbeat(cfg HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Heartbeat refreshes the idle watchdog (spec 02 §3.3: the frontend
		// heartbeats every 30s; the middleware also kicks on this request).
		if cfg.Kick != nil {
			cfg.Kick()
		}
		if cfg.State != nil {
			cfg.State.Heartbeat(time.Now())
		}
		// spec 07 §3.1 response shape.
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":    "ALIVE",
			"timestamp": time.Now().UnixMilli(),
		})
	}
}

func sessionShutdown(cfg HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Accept the request, then trigger graceful shutdown. The
		// SYSTEM_SHUTDOWN broadcast to SSE clients is the WebServer's
		// responsibility (server.Config.EventSink — wired to the client bus in
		// cmd/web): the server broadcasts through it in its Shutdown path for
		// ALL shutdown origins (watchdog, OS signal, this endpoint), so the
		// event reaches every client exactly once (qc3-M2). The response is
		// flushed before shutdown begins (spec 02 §2.1 + §3.1 event #6).
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "SHUTTING_DOWN"})

		if cfg.Shutdown == nil {
			return
		}
		// Give the 202 response a moment to flush, then shut down with the
		// standard 5s write-drain budget (spec 02 §3.3).
		go func() {
			time.Sleep(250 * time.Millisecond)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = cfg.Shutdown(ctx)
		}()
	}
}
