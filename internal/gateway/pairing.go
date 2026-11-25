package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/AriOliv/whatsapp-mcp/internal/store"
)

// Bindings is the persistent owner <-> number table.
type Bindings interface {
	AccountOf(ctx context.Context, owner string) (string, error)
	OwnerOf(ctx context.Context, account string) (string, error)
	Bind(ctx context.Context, owner, account string) error
	Unbind(ctx context.Context, owner string) error
}

// Linker drives WhatsApp: phone-code pairing and full account removal.
type Linker interface {
	PairPhone(ctx context.Context, phone string, onCode func(string), approve func(account string) error) (string, error)
	Purge(ctx context.Context, account string) (unlinked bool, err error)
	AccountState(account string) string
}

const (
	defaultPairingTTL  = 3 * time.Minute
	defaultMaxPairings = 10
	codeWait           = 45 * time.Second
	// pairingCooldown spaces out attempts by one owner: each attempt makes
	// WhatsApp notify the target phone, so retries must not become spam.
	pairingCooldown = 20 * time.Second
	minPhoneDigits  = 8
	maxPhoneDigits  = 15
)

// LinkInstructions tells the user where to type the pairing code.
const LinkInstructions = "On the phone: WhatsApp > Settings > Linked devices > Link a device > " +
	"Link with phone number instead, then type the code."

// Pairing runs phone-code pairing on behalf of gateway owners. Each owner has
// at most one flow in progress, flows expire after a TTL, and the number of
// concurrent flows is capped.
type Pairing struct {
	bindings Bindings
	linker   Linker
	ttl      time.Duration
	max      int
	now      func() time.Time

	mu    sync.Mutex
	flows map[string]*flow
}

type flow struct {
	phone   string
	started time.Time
	expires time.Time
	cancel  context.CancelFunc
	codeCh  chan struct{}
	done    chan struct{}

	mu   sync.Mutex
	code string
	err  error
}

func (f *flow) snapshot() (code string, finished bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-f.done:
		finished = true
	default:
	}
	return f.code, finished, f.err
}

// NewPairing builds the pairing service.
func NewPairing(b Bindings, l Linker) *Pairing {
	return &Pairing{bindings: b, linker: l, ttl: defaultPairingTTL, max: defaultMaxPairings, now: time.Now,
		flows: map[string]*flow{}}
}

// ConnectResult is what whatsapp_connect returns.
type ConnectResult struct {
	Phone        string    `json:"phone"`
	Code         string    `json:"pairing_code"`
	ExpiresAt    time.Time `json:"expires_at"`
	Instructions string    `json:"instructions"`
}

// Connect starts pairing phone for owner and returns the code to type on the
// phone. The binding is written only once WhatsApp confirms the link.
func (p *Pairing) Connect(ctx context.Context, owner, phone string) (ConnectResult, error) {
	phone = digitsOnly(phone)
	if len(phone) < minPhoneDigits || len(phone) > maxPhoneDigits {
		return ConnectResult{}, errors.New("phone_number must be the full international number, e.g. 5511912345678")
	}
	current, err := p.bindings.AccountOf(ctx, owner)
	if err != nil {
		return ConnectResult{}, err
	}
	if current == phone {
		return ConnectResult{}, errors.New("this number is already connected to your account")
	}
	if current != "" {
		return ConnectResult{}, errors.New("you already have a WhatsApp number connected; run whatsapp_disconnect first")
	}
	other, err := p.bindings.OwnerOf(ctx, phone)
	if err != nil {
		return ConnectResult{}, err
	}
	if other != "" {
		return ConnectResult{}, store.ErrAccountBound
	}

	f, err := p.startFlow(owner, phone)
	if err != nil {
		return ConnectResult{}, err
	}
	return p.awaitCode(ctx, f)
}

func (p *Pairing) startFlow(owner, phone string) (*flow, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked()
	if f, ok := p.flows[owner]; ok {
		if _, finished, _ := f.snapshot(); !finished {
			if f.phone == phone {
				return f, nil
			}
			return nil, fmt.Errorf("a pairing for another number is in progress; it expires at %s",
				f.expires.UTC().Format(time.RFC3339))
		}
		if wait := f.started.Add(pairingCooldown).Sub(p.now()); wait > 0 {
			return nil, fmt.Errorf("wait %s before starting another pairing", wait.Round(time.Second))
		}
	}
	active := 0
	for o, f := range p.flows {
		if _, finished, _ := f.snapshot(); finished {
			continue
		}
		if f.phone == phone && o != owner {
			return nil, errors.New("a pairing for this number is already in progress")
		}
		active++
	}
	if active >= p.max {
		return nil, errors.New("too many pairings in progress; try again in a few minutes")
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.ttl)
	now := p.now()
	f := &flow{phone: phone, started: now, expires: now.Add(p.ttl), cancel: cancel,
		codeCh: make(chan struct{}), done: make(chan struct{})}
	p.flows[owner] = f
	go p.run(ctx, owner, f)
	return f, nil
}

func (p *Pairing) run(ctx context.Context, owner string, f *flow) {
	defer f.cancel()
	var codeOnce sync.Once
	onCode := func(code string) {
		f.mu.Lock()
		f.code = code
		f.mu.Unlock()
		codeOnce.Do(func() { close(f.codeCh) })
	}
	approve := func(account string) error {
		if account != f.phone {
			return errors.New("the phone that confirmed the code is not the number requested")
		}
		return p.bindings.Bind(context.Background(), owner, account)
	}
	_, err := p.linker.PairPhone(ctx, f.phone, onCode, approve)
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
	close(f.done)
}

func (p *Pairing) awaitCode(ctx context.Context, f *flow) (ConnectResult, error) {
	wait := time.NewTimer(codeWait)
	defer wait.Stop()
	select {
	case <-f.codeCh:
	case <-f.done:
	case <-wait.C:
		f.cancel()
		return ConnectResult{}, errors.New("WhatsApp did not issue a pairing code in time; try again")
	case <-ctx.Done():
		return ConnectResult{}, ctx.Err()
	}
	code, _, err := f.snapshot()
	if code == "" {
		if err == nil {
			err = errors.New("pairing ended before a code was issued")
		}
		return ConnectResult{}, fmt.Errorf("pairing failed: %w", err)
	}
	return ConnectResult{Phone: f.phone, Code: code, ExpiresAt: f.expires, Instructions: LinkInstructions}, nil
}

// pruneLocked forgets flows that finished or expired more than a TTL ago, so
// status can still report a recent failure.
func (p *Pairing) pruneLocked() {
	cutoff := p.now().Add(-p.ttl)
	for owner, f := range p.flows {
		if _, finished, _ := f.snapshot(); finished && f.expires.Before(cutoff) {
			delete(p.flows, owner)
		}
	}
}

// Status is what whatsapp_connection_status returns.
type Status struct {
	Connected bool           `json:"connected"`
	Phone     string         `json:"phone,omitempty"`
	State     string         `json:"state"`
	Pairing   *PairingStatus `json:"pairing,omitempty"`
}

// PairingStatus describes the caller's latest pairing flow.
type PairingStatus struct {
	Phone     string    `json:"phone"`
	State     string    `json:"state"`
	ExpiresAt time.Time `json:"expires_at"`
	Error     string    `json:"error,omitempty"`
}

// Status reports owner's binding and any recent pairing flow.
func (p *Pairing) Status(ctx context.Context, owner string) (Status, error) {
	account, err := p.bindings.AccountOf(ctx, owner)
	if err != nil {
		return Status{}, err
	}
	st := Status{State: "not_connected"}
	if account != "" {
		st = Status{Connected: true, Phone: account, State: p.linker.AccountState(account)}
	}
	p.mu.Lock()
	p.pruneLocked()
	f := p.flows[owner]
	p.mu.Unlock()
	if f != nil {
		_, finished, ferr := f.snapshot()
		ps := &PairingStatus{Phone: f.phone, State: "waiting_for_code_entry", ExpiresAt: f.expires}
		switch {
		case finished && ferr == nil:
			ps.State = "paired"
		case finished:
			ps.State, ps.Error = "failed", ferr.Error()
		}
		st.Pairing = ps
	}
	return st, nil
}

// DisconnectResult is what whatsapp_disconnect returns.
type DisconnectResult struct {
	Phone       string `json:"phone,omitempty"`
	Unlinked    bool   `json:"unlinked_from_whatsapp"`
	DataDeleted bool   `json:"data_deleted"`
	Note        string `json:"note,omitempty"`
}

// Disconnect cancels owner's pairing flow, unlinks and deletes everything for
// owner's number, and removes the binding.
func (p *Pairing) Disconnect(ctx context.Context, owner string) (DisconnectResult, error) {
	p.mu.Lock()
	if f, ok := p.flows[owner]; ok {
		f.cancel()
		delete(p.flows, owner)
	}
	p.mu.Unlock()

	account, err := p.bindings.AccountOf(ctx, owner)
	if err != nil {
		return DisconnectResult{}, err
	}
	if account == "" {
		return DisconnectResult{Note: "no WhatsApp number was connected"}, nil
	}
	unlinked, err := p.linker.Purge(ctx, account)
	if err != nil {
		return DisconnectResult{Phone: account, Unlinked: unlinked}, fmt.Errorf("disconnect failed, try again: %w", err)
	}
	if err := p.bindings.Unbind(ctx, owner); err != nil {
		return DisconnectResult{Phone: account, Unlinked: unlinked, DataDeleted: true}, err
	}
	res := DisconnectResult{Phone: account, Unlinked: unlinked, DataDeleted: true}
	if !unlinked {
		res.Note = "WhatsApp could not be reached; remove the device under Linked devices on the phone"
	}
	return res, nil
}

func digitsOnly(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			b = append(b, s[i])
		}
	}
	return string(b)
}
