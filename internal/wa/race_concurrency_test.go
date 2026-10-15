package wa

import (
	"sync"
	"testing"

	"go.mau.fi/whatsmeow"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// Health() reads the client map and the counters while the event handler writes
// them. Run both hard under -race to catch an unsynchronised field.
func TestHealthConcurrentWithConnectEvents(t *testing.T) {
	m := &Manager{
		clients:            map[string]*whatsmeow.Client{},
		perAccountConnects: map[string]int{},
		log:                waLog.Noop,
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				m.connects.Add(1)
				m.mu.Lock()
				m.perAccountConnects["5511999999999"]++
				m.mu.Unlock()
			}
		}()
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				_, _, _ = m.Health()
			}
		}()
	}
	wg.Wait()
	if got := m.connects.Load(); got != 16000 {
		t.Fatalf("lost counter increments: want 16000, got %d", got)
	}
	m.mu.RLock()
	per := m.perAccountConnects["5511999999999"]
	m.mu.RUnlock()
	if per != 16000 {
		t.Fatalf("lost per-account increments: want 16000, got %d", per)
	}
}
