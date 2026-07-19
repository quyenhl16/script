package ui

import (
	"sync"
)

type safeBuffer struct {
	mu   sync.RWMutex
	data []byte
}

func (b *safeBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, data...)
	return len(data), nil
}

func (b *safeBuffer) String() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return string(append([]byte(nil), b.data...))
}
