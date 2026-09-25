package wa

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"

	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
)

// countingLog records what was logged so a test can assert on throttling.
type countingLog struct {
	mu    sync.Mutex
	warns []string
}

func (l *countingLog) Warnf(msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, fmt.Sprintf(msg, args...))
}
func (l *countingLog) Errorf(string, ...any)   {}
func (l *countingLog) Infof(string, ...any)    {}
func (l *countingLog) Debugf(string, ...any)   {}
func (l *countingLog) Sub(string) waLog.Logger { return l }
func (l *countingLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.warns)
}

// newTestStore opens a throwaway SQLite store on disk (in-memory databases are
// per-connection, and the pool opens more than one).
func newTestStore(t *testing.T) *appstore.Store {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "test.db") +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	st, _, err := appstore.Open(dsn, false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}
	return st
}

func TestIngestWriterStoresReceivedMessages(t *testing.T) {
	st := newTestStore(t)
	m := &Manager{store: st, log: &countingLog{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.startIngest(ctx)

	const want = 25
	for i := range want {
		m.enqueueMessage("5511999999999", appstore.Message{
			ID:      fmt.Sprintf("msg-%02d", i),
			ChatJID: "5511888888888@s.whatsapp.net",
			TS:      int64(i),
			Body:    "hello",
		})
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := st.ListMessages(ctx, "5511999999999", "5511888888888@s.whatsapp.net", 100)
		if err != nil {
			t.Fatalf("list messages: %v", err)
		}
		if len(got) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("writer stored %d of %d messages", len(got), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The whole point of the queue: receiving a message must never wait on the
// database, because whatsmeow dispatches handlers inside its serial node loop.
func TestEnqueueDoesNotBlockWhenTheWriterIsStuck(t *testing.T) {
	m := &Manager{log: &countingLog{}}
	m.ingest = make(chan ingestItem, 2) // no writer draining it

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 100 {
			m.enqueueMessage("acct", appstore.Message{ID: fmt.Sprintf("m%d", i)})
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("enqueueMessage blocked when the queue was full")
	}
	if got := m.ingestDropped.Load(); got != 98 {
		t.Fatalf("dropped %d messages, want 98 (100 sent, 2 buffered)", got)
	}
}

func TestShutdownFlushesQueuedMessages(t *testing.T) {
	st := newTestStore(t)
	m := &Manager{store: st, log: &countingLog{}}
	ctx, cancel := context.WithCancel(context.Background())
	m.startIngest(ctx)

	for i := range 10 {
		m.enqueueMessage("acct", appstore.Message{
			ID:      fmt.Sprintf("flush-%d", i),
			ChatJID: "chat@s.whatsapp.net",
			TS:      int64(i),
		})
	}
	cancel() // shutdown while writes are still queued

	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := st.ListMessages(context.Background(), "acct", "chat@s.whatsapp.net", 100)
		if err != nil {
			t.Fatalf("list messages: %v", err)
		}
		if len(got) == 10 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("shutdown flushed %d of 10 messages", len(got))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A database outage affects every message; one log line per message would bury
// everything else in the log.
func TestWarnThrottledLogsOncePerWindow(t *testing.T) {
	log := &countingLog{}
	m := &Manager{log: log}
	for range 500 {
		m.warnThrottled(&m.ingestDropLog, "queue full")
	}
	if got := log.count(); got != 1 {
		t.Fatalf("logged %d times in one window, want 1", got)
	}
}
