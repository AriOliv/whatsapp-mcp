package wa

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
)

// unlinkTimeout bounds waiting for a socket and sending the logout request.
const unlinkTimeout = 15 * time.Second

// Purge unlinks account from WhatsApp and deletes everything stored for it:
// every whatsmeow device row of that number (sessions, keys, contacts, app
// state cascade from it) and this server's history, chats, forms, webhook
// outbox and logout marker. unlinked is false when WhatsApp could not be told
// (socket down); the local data is deleted either way and the phone may still
// list the device until the user removes it there.
func (m *Manager) Purge(ctx context.Context, account string) (unlinked bool, err error) {
	account = onlyDigits(account)
	if account == "" {
		return false, fmt.Errorf("purge: invalid account")
	}
	m.mu.Lock()
	if m.purged == nil {
		m.purged = map[string]bool{}
	}
	m.purged[account] = true
	cli := m.clients[account]
	delete(m.clients, account)
	delete(m.loggedOut, account)
	delete(m.perAccountConnects, account)
	if m.def == account {
		m.def = ""
	}
	m.mu.Unlock()

	if cli != nil {
		unlinked = m.unlink(ctx, cli)
	}
	if m.container != nil {
		devices, err := m.container.GetAllDevices(ctx)
		if err != nil {
			return unlinked, fmt.Errorf("purge: list devices: %w", err)
		}
		for _, dev := range devices {
			if accountKey(dev.ID) != account {
				continue
			}
			if err := m.container.DeleteDevice(ctx, dev); err != nil {
				return unlinked, fmt.Errorf("purge: delete device: %w", err)
			}
		}
	}
	if m.store != nil {
		if err := m.store.DeleteAccountData(ctx, account); err != nil {
			return unlinked, fmt.Errorf("purge: %w", err)
		}
	}
	return unlinked, nil
}

// unlink asks WhatsApp to remove cli's companion device, waiting briefly for a
// socket if it is mid-reconnect. On failure the client is just disconnected.
func (m *Manager) unlink(ctx context.Context, cli *whatsmeow.Client) bool {
	ctx, cancel := context.WithTimeout(ctx, unlinkTimeout)
	defer cancel()
	if !cli.IsConnected() || !cli.IsLoggedIn() {
		cli.WaitForConnection(unlinkTimeout)
	}
	if err := cli.Logout(ctx); err != nil {
		m.log.Warnf("logout during purge failed, deleting local data anyway: %v", err)
		cli.Disconnect()
		return false
	}
	return true
}

// discardDevice undoes a pairing that was not approved: the device is unlinked
// if possible and its whatsmeow rows deleted.
func (m *Manager) discardDevice(cli *whatsmeow.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), unlinkTimeout)
	defer cancel()
	if m.unlink(ctx, cli) {
		return
	}
	if cli.Store != nil && cli.Store.ID != nil {
		if err := cli.Store.Delete(ctx); err != nil {
			m.log.Errorf("deleting a rejected device failed: %v", err)
		}
	}
}

func (m *Manager) isPurged(account string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.purged[account]
}

// AccountNotLinked is the state of an account with no client and no logout marker.
const AccountNotLinked = "not_linked"

// AccountState reports one account's connection state without blocking.
func (m *Manager) AccountState(account string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if cli := m.clients[account]; cli != nil {
		if cli.IsConnected() && cli.IsLoggedIn() {
			return AccountConnected
		}
		return AccountReconnecting
	}
	if _, ok := m.loggedOut[account]; ok {
		return AccountNeedsRepair
	}
	return AccountNotLinked
}
