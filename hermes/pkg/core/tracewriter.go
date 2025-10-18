package core

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
)

type JSONTraceWriter struct {
	mu sync.Mutex
	w  *bufio.Writer
	f  *os.File
}

func NewJSONTraceWriter(path string) (*JSONTraceWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &JSONTraceWriter{w: bufio.NewWriter(f), f: f}, nil
}

func (tw *JSONTraceWriter) Record(dt DecisionTrace) error {
	if tw == nil || tw.w == nil {
		return nil
	}
	tw.mu.Lock()
	defer tw.mu.Unlock()
	enc, err := json.Marshal(dt)
	if err != nil {
		return err
	}
	if _, err := tw.w.Write(enc); err != nil {
		return err
	}
	if err := tw.w.WriteByte('\n'); err != nil {
		return err
	}
	return tw.w.Flush()
}

func (tw *JSONTraceWriter) Close() error {
	if tw == nil || tw.f == nil {
		return nil
	}
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.w != nil {
		if err := tw.w.Flush(); err != nil {
			_ = tw.f.Close()
			return err
		}
	}
	return tw.f.Close()
}
