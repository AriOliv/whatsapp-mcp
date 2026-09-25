package wa

import (
	"context"
	"sync/atomic"
	"time"

	appstore "github.com/AriOliv/whatsapp-mcp/internal/store"
)

// whatsmeow runs event handlers synchronously (Client.dispatchEvent) inside its
// serial node-handling loop, which warns at 30s and abandons the node after five
// minutes (whatsmeow client.go, handlerQueueLoop). Saving a received message
// straight from the handler therefore puts a database round-trip on the
// message-reception path: when the database slows down — a reconnect storm after
// a deploy, with every linked account replaying history over the pool this
// process shares with whatsmeow's own sqlstore — reception stalls for *all*
// accounts, not just for the write.
//
// The queue below decouples the two. Receiving costs a channel send, so reception
// stays bounded by the socket rather than by the database.
const (
	// ingestQueueSize is how many received messages may wait on the database.
	// Roughly a minute of a heavy history sync; past it we drop rather than block.
	ingestQueueSize = 4096
	// ingestWriteTimeout bounds a single write so one wedged statement cannot
	// stop the writer for good.
	ingestWriteTimeout = 15 * time.Second
	// ingestLogEvery throttles the failure logs: a database outage affects every
	// message, and one line per message would bury everything else.
	ingestLogEvery = 30 * time.Second
)

// ingestItem is a received message waiting to be stored, with the account that
// received it.
type ingestItem struct {
	account string
	msg     appstore.Message
}

// startIngest launches the single writer that drains received messages into the
// app store. It flushes whatever is queued when ctx is cancelled, so a shutdown
// does not discard the backlog.
func (m *Manager) startIngest(ctx context.Context) {
	m.ingest = make(chan ingestItem, ingestQueueSize)
	go func() {
		for {
			select {
			case it := <-m.ingest:
				m.writeMessage(it)
			case <-ctx.Done():
				for {
					select {
					case it := <-m.ingest:
						m.writeMessage(it)
					default:
						return
					}
				}
			}
		}
	}()
}

// enqueueMessage hands a received message to the writer. It never blocks: when
// the queue is full the message is dropped, so a database problem costs stored
// history instead of freezing reception.
func (m *Manager) enqueueMessage(account string, msg appstore.Message) {
	select {
	case m.ingest <- ingestItem{account: account, msg: msg}:
	default:
		n := m.ingestDropped.Add(1)
		m.warnThrottled(&m.ingestDropLog, "message ingest queue full; %d message(s) dropped so far", n)
	}
}

// writeMessage stores one message. It builds its own context because the
// writer's context is already cancelled during the shutdown flush.
func (m *Manager) writeMessage(it ingestItem) {
	ctx, cancel := context.WithTimeout(context.Background(), ingestWriteTimeout)
	defer cancel()
	if err := m.store.SaveMessage(ctx, it.account, it.msg); err != nil {
		n := m.ingestFailed.Add(1)
		m.warnThrottled(&m.ingestFailLog, "storing received messages is failing (%d so far), last error: %v", n, err)
	}
}

// warnThrottled logs at most once per ingestLogEvery, keyed by the given
// timestamp, so a sustained failure reports its running count instead of one
// line per message.
func (m *Manager) warnThrottled(last *atomic.Int64, format string, args ...any) {
	now := time.Now().UnixNano()
	prev := last.Load()
	if now-prev < int64(ingestLogEvery) {
		return
	}
	if !last.CompareAndSwap(prev, now) {
		return // another goroutine just logged
	}
	m.log.Warnf(format, args...)
}
