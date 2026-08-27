package wa

import (
	"context"
	"fmt"
	"sync"

	"go.mau.fi/whatsmeow"
)

// PairFlow is a single in-progress web pairing (one per OAuth authorize flow).
// A background goroutine reads the whatsmeow QR channel and updates the fields;
// the login page polls snapshot()/RequestPhoneCode().
type PairFlow struct {
	mu       sync.Mutex
	qrCode   string // current rotating QR payload (render to PNG for the page)
	pairCode string // 8-digit phone code, once RequestPhoneCode is called
	state    string // "pairing" | "open" | "timeout" | "error"
	sub      string // captured phone number on success
	errMsg   string
	cli      *whatsmeow.Client
	cancel   context.CancelFunc
}

// Snapshot returns the flow's current state for the login page.
func (f *PairFlow) Snapshot() (qr, pairCode, state, sub, errMsg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.qrCode, f.pairCode, f.state, f.sub, f.errMsg
}

// RequestPhoneCode switches this flow to phone-code pairing and returns the
// 8-digit code to type into WhatsApp.
func (f *PairFlow) RequestPhoneCode(ctx context.Context, number string) (string, error) {
	digits := onlyDigits(number)
	if digits == "" {
		return "", fmt.Errorf("invalid phone number")
	}
	f.mu.Lock()
	cli := f.cli
	f.mu.Unlock()
	if cli == nil {
		return "", fmt.Errorf("pairing flow not ready")
	}
	code, err := cli.PairPhone(ctx, digits, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	f.pairCode = code
	f.mu.Unlock()
	return code, nil
}

// StartFlow begins a new pairing flow keyed by flowID and returns it. A
// goroutine drives the QR channel until success/timeout/error.
func (m *Manager) StartFlow(flowID string) (*PairFlow, error) {
	dev := m.container.NewDevice()
	cli := whatsmeow.NewClient(dev, m.log)
	ctx, cancel := context.WithCancel(context.Background())
	qrChan, err := cli.GetQRChannel(ctx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("qr channel: %w", err)
	}
	f := &PairFlow{state: "pairing", cli: cli, cancel: cancel}
	m.mu.Lock()
	m.flows[flowID] = f
	m.mu.Unlock()
	if err := cli.Connect(); err != nil {
		cancel()
		return nil, fmt.Errorf("connect: %w", err)
	}
	go func() {
		for evt := range qrChan {
			switch evt.Event {
			case "code":
				f.mu.Lock()
				f.qrCode = evt.Code
				f.mu.Unlock()
			case "success":
				cli.AddEventHandler(m.handler(cli))
				key := accountKey(cli.Store.ID)
				m.mu.Lock()
				m.clients[key] = cli
				if m.def == "" {
					m.def = key
				}
				m.mu.Unlock()
				f.mu.Lock()
				f.state = "open"
				f.sub = key
				f.mu.Unlock()
				return
			case "timeout":
				f.mu.Lock()
				f.state = "timeout"
				f.mu.Unlock()
				return
			case "error":
				f.mu.Lock()
				f.state = "error"
				if evt.Error != nil {
					f.errMsg = evt.Error.Error()
				}
				f.mu.Unlock()
				return
			}
		}
	}()
	return f, nil
}

// Flow returns an in-progress pairing flow by id.
func (m *Manager) Flow(flowID string) (*PairFlow, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, ok := m.flows[flowID]
	return f, ok
}

// EndFlow cancels and forgets a pairing flow (call after success/abandon).
func (m *Manager) EndFlow(flowID string) {
	m.mu.Lock()
	f := m.flows[flowID]
	delete(m.flows, flowID)
	m.mu.Unlock()
	if f != nil && f.cancel != nil {
		f.cancel()
	}
}
