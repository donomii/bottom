package main

import "log"

func logBackendFallback(logger *log.Logger, backend string, err error) {
	logger.Printf("backend=%s fallback=%s reason=%v", backend, BackendPoll, err)
}
