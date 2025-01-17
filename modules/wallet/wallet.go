package wallet

// TODO: Theoretically, the transaction builder in this wallet supports
// multisig, but there are no automated tests to verify that.

import (
	"bytes"
	"fmt"
	"sort"
	"sync"

	"gitlab.com/NebulousLabs/bolt"

	"SiaPrime/build"
	"SiaPrime/crypto"
	"SiaPrime/encoding"
	"SiaPrime/modules"
	"SiaPrime/persist"
	siasync "SiaPrime/sync"
	"SiaPrime/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/threadgroup"
)

const (
	// RespendTimeout records the number of blocks that the wallet will wait
	// before spending an output that has been spent in the past. If the
	// transaction spending the output has not made it to the transaction pool
	// after the limit, the assumption is that it never will.
	RespendTimeout = 40
)

var (
	errNilConsensusSet = errors.New("wallet cannot initialize with a nil consensus set")
	errNilTpool        = errors.New("wallet cannot initialize with a nil transaction pool")
)

// spendableKey is a set of secret keys plus the corresponding unlock
// conditions.  The public key can be derived from the secret key and then
// matched to the corresponding public keys in the unlock conditions. All
// addresses that are to be used in 'FundSiacoins' or 'FundSiafunds' in the
// transaction builder must conform to this form of spendable key.
type spendableKey struct {
	UnlockConditions types.UnlockConditions
	SecretKeys       []crypto.SecretKey
}

// Wallet is an object that tracks balances, creates keys and addresses,
// manages building and sending transactions.
type Wallet struct {
	// encrypted indicates whether the wallet has been encrypted (i.e.
	// initialized). unlocked indicates whether the wallet is currently
	// storing secret keys in memory. subscribed indicates whether the wallet
	// has subscribed to the consensus set yet - the wallet is unable to
	// subscribe to the consensus set until it has been unlocked for the first
	// time. The primary seed is used to generate new addresses for the
	// wallet.
	encrypted   bool
	unlocked    bool
	subscribed  bool
	primarySeed modules.Seed

	// The wallet's dependencies.
	cs    modules.ConsensusSet
	tpool modules.TransactionPool
	deps  modules.Dependencies

	// The following set of fields are responsible for tracking the confirmed
	// outputs, and for being able to spend them. The seeds are used to derive
	// the keys that are tracked on the blockchain. All keys are pregenerated
	// from the seeds, when checking new outputs or spending outputs, the seeds
	// are not referenced at all. The seeds are only stored so that the user
	// may access them.
	seeds        []modules.Seed
	keys         map[types.UnlockHash]spendableKey
	lookahead    map[types.UnlockHash]uint64
	watchedAddrs map[types.UnlockHash]struct{}

	// unconfirmedProcessedTransactions tracks unconfirmed transactions.
	//
	// TODO: Replace this field with a linked list. Currently when a new
	// transaction set diff is provided, the entire array needs to be
	// reallocated. Since this can happen tens of times per second, and the
	// array can have tens of thousands of elements, it's a performance issue.
	unconfirmedSets                  map[modules.TransactionSetID][]types.TransactionID
	unconfirmedProcessedTransactions []modules.ProcessedTransaction

	// The wallet's database tracks its seeds, keys, outputs, and
	// transactions. A global db transaction is maintained in memory to avoid
	// excessive disk writes. Any operations involving dbTx must hold an
	// exclusive lock.
	//
	// If dbRollback is set, then when the database syncs it will perform a
	// rollback instead of a commit. For safety reasons, the db will close and
	// the wallet will close if a rollback is performed.
	db         *persist.BoltDatabase
	dbRollback bool
	dbTx       *bolt.Tx

	persistDir string
	log        *persist.Logger
	mu         sync.RWMutex

	// A separate TryMutex is used to protect against concurrent unlocking or
	// initialization.
	scanLock siasync.TryMutex

	// The wallet's ThreadGroup tells tracked functions to shut down and
	// blocks until they have all exited before returning from Close.
	tg threadgroup.ThreadGroup

	// defragDisabled determines if the wallet is set to defrag outputs once it
	// reaches a certain threshold
	defragDisabled bool
}

// Height return the internal processed consensus height of the wallet
func (w *Wallet) Height() (types.BlockHeight, error) {
	if err := w.tg.Add(); err != nil {
		return types.BlockHeight(0), modules.ErrWalletShutdown
	}
	defer w.tg.Done()

	w.mu.Lock()
	defer w.mu.Unlock()
	w.syncDB()

	var height uint64
	err := w.db.View(func(tx *bolt.Tx) error {
		return encoding.Unmarshal(tx.Bucket(bucketWallet).Get(keyConsensusHeight), &height)
	})
	if err != nil {
		return types.BlockHeight(0), err
	}
	return types.BlockHeight(height), nil
}

// New creates a new wallet, loading any known addresses from the input file
// name and then using the file to save in the future. Keys and addresses are
// not loaded into the wallet during the call to 'new', but rather during the
// call to 'Unlock'.
func New(cs modules.ConsensusSet, tpool modules.TransactionPool, persistDir string) (*Wallet, error) {
	return NewCustomWallet(cs, tpool, persistDir, modules.ProdDependencies)
}

// NewCustomWallet creates a new wallet using custom dependencies.
func NewCustomWallet(cs modules.ConsensusSet, tpool modules.TransactionPool, persistDir string, deps modules.Dependencies) (*Wallet, error) {
	// Check for nil dependencies.
	if cs == nil {
		return nil, errNilConsensusSet
	}
	if tpool == nil {
		return nil, errNilTpool
	}

	// Initialize the data structure.
	w := &Wallet{
		cs:    cs,
		tpool: tpool,

		keys:         make(map[types.UnlockHash]spendableKey),
		lookahead:    make(map[types.UnlockHash]uint64),
		watchedAddrs: make(map[types.UnlockHash]struct{}),

		unconfirmedSets: make(map[modules.TransactionSetID][]types.TransactionID),

		persistDir: persistDir,

		deps: deps,
	}
	err := w.initPersist()
	if err != nil {
		return nil, err
	}
	return w, nil
}

// Close terminates all ongoing processes involving the wallet, enabling
// garbage collection.
func (w *Wallet) Close() error {
	if err := w.tg.Stop(); err != nil {
		return err
	}
	var errs []error
	// Lock the wallet outside of mu.Lock because Lock uses its own mu.Lock.
	// Once the wallet is locked it cannot be unlocked except using the
	// unexported unlock method (w.Unlock returns an error if the wallet's
	// ThreadGroup is stopped).
	if w.managedUnlocked() {
		if err := w.managedLock(); err != nil {
			errs = append(errs, err)
		}
	}

	w.cs.Unsubscribe(w)
	w.tpool.Unsubscribe(w)

	if err := w.log.Close(); err != nil {
		errs = append(errs, fmt.Errorf("log.Close failed: %v", err))
	}
	return build.JoinErrors(errs, "; ")
}

// AllAddresses returns all addresses that the wallet is able to spend from,
// including unseeded addresses. Addresses are returned sorted in byte-order.
func (w *Wallet) AllAddresses() ([]types.UnlockHash, error) {
	if err := w.tg.Add(); err != nil {
		return []types.UnlockHash{}, modules.ErrWalletShutdown
	}
	defer w.tg.Done()

	w.mu.RLock()
	defer w.mu.RUnlock()

	addrs := make([]types.UnlockHash, 0, len(w.keys))
	for addr := range w.keys {
		addrs = append(addrs, addr)
	}
	sort.Slice(addrs, func(i, j int) bool {
		return bytes.Compare(addrs[i][:], addrs[j][:]) < 0
	})
	return addrs, nil
}

// Rescanning reports whether the wallet is currently rescanning the
// blockchain.
func (w *Wallet) Rescanning() (bool, error) {
	if err := w.tg.Add(); err != nil {
		return false, modules.ErrWalletShutdown
	}
	defer w.tg.Done()

	rescanning := !w.scanLock.TryLock()
	if !rescanning {
		w.scanLock.Unlock()
	}
	return rescanning, nil
}

// Settings returns the wallet's current settings
func (w *Wallet) Settings() (modules.WalletSettings, error) {
	if err := w.tg.Add(); err != nil {
		return modules.WalletSettings{}, modules.ErrWalletShutdown
	}
	defer w.tg.Done()
	return modules.WalletSettings{
		NoDefrag: w.defragDisabled,
	}, nil
}

// SetSettings will update the settings for the wallet.
func (w *Wallet) SetSettings(s modules.WalletSettings) error {
	if err := w.tg.Add(); err != nil {
		return modules.ErrWalletShutdown
	}
	defer w.tg.Done()

	w.mu.Lock()
	w.defragDisabled = s.NoDefrag
	w.mu.Unlock()
	return nil
}
