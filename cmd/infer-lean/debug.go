package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

var debugState = struct {
	mu      sync.RWMutex
	enabled bool
	writer  io.Writer
}{
	enabled: false,
	writer:  io.Discard,
}

func configureDebug(enabled bool, writer io.Writer) {
	debugState.mu.Lock()
	defer debugState.mu.Unlock()
	debugState.enabled = enabled
	if writer == nil {
		debugState.writer = io.Discard
		return
	}
	debugState.writer = writer
}

func debugf(format string, args ...any) {
	debugState.mu.RLock()
	enabled := debugState.enabled
	writer := debugState.writer
	debugState.mu.RUnlock()
	if !enabled || writer == nil {
		return
	}

	ts := time.Now().UTC().Format(time.RFC3339Nano)
	line := fmt.Sprintf(format, args...)

	debugState.mu.Lock()
	defer debugState.mu.Unlock()
	_, _ = fmt.Fprintf(writer, "[debug %s] %s\n", ts, line)
}

func debugSnippet(text string, limit int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if limit <= 0 {
		limit = 800
	}
	if len(text) <= limit {
		return text
	}
	return text[:limit] + " ... (truncated)"
}
