package pool

import (
	//"github.com/sasha-s/go-deadlock"
	"sync"
    "sync/atomic"

	"SiaPrime/persist"
	"SiaPrime/types"
)

//
// A ClientRecord represents the persistent data portion of the Client record
//
type ClientRecord struct {
	clientID int64
	name     string
	wallet   types.UnlockHash
}

//
// A Client represents a user and may have one or more workers associated with it.  It is primarily used for
// accounting and statistics.
//
type Client struct {
	cr   ClientRecord
	mu   sync.RWMutex
	pool *Pool
	log  *persist.Logger
}

// newClient creates a new Client record
func newClient(p *Pool, name string) (*Client, error) {
	// id := p.newStratumID()
	c := &Client{
		cr: ClientRecord{
			name: name,
		},
		pool: p,
        log: p.log,
	}
	c.cr.wallet.LoadString(name)
	// check if this worker instance is an original or copy
	// TODO why do we need to make a copy instead of the original?
	if p.Client(name) != nil {
		//return c, nil
		return p.Client(name), nil
	}

	return c, nil
}

// Name returns the client's name, which is usually the wallet address
func (c *Client) Name() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.cr.name
}

// SetName sets the client's name
func (c *Client) SetName(n string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cr.name = n

}

func (c *Client) SetID(id int64) {
    atomic.StoreInt64(&c.cr.clientID, id)
}

func (c *Client) GetID() int64 {
    return atomic.LoadInt64(&c.cr.clientID)
}

// Wallet returns the unlockhash associated with the client
func (c *Client) Wallet() *types.UnlockHash {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return &c.cr.wallet
}

// SetWallet sets the unlockhash associated with the client
func (c *Client) SetWallet(w types.UnlockHash) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cr.wallet = w
}

// Pool returns the client's pool
func (c *Client) Pool() *Pool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pool
}
