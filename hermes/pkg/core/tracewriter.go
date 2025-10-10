package core

import (
	"encoding/json"
	"os"
	"sync"
)

// JSONTraceWriter appends decision traces to a JSONL file.
type JSONTraceWriter struct {
	mu  sync.Mutex
	enc *json.Encoder
	f   *os.File
}

// NewJSONTraceWriter creates a writer that appends to path (creating it if needed).
func NewJSONTraceWriter(path string) (*JSONTraceWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &JSONTraceWriter{
		enc: json.NewEncoder(f),
		f:   f,
	}, nil
}

// Record writes the trace as one JSON object per line.
func (w *JSONTraceWriter) Record(trace DecisionTrace) {
	if w == nil || w.enc == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.enc.Encode(trace)
}

// Close releases the underlying file handle.
func (w *JSONTraceWriter) Close() error {
	if w == nil || w.f == nil {
		return nil
	}
	return w.f.Close()
}
