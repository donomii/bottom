package main

import "log"

func logBackendFallback(logger *log.Logger, backend string, err error) {
	logger.Printf("backend=%s fallback=%s reason=%v", backend, BackendPoll, err)
}

func logBackendDiagnostic(logger *log.Logger, event Event) {
	logger.Printf("backend=%s diagnostic=%s", event.Backend, event.Message)
}
