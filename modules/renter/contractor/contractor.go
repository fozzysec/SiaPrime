package contractor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"SiaPrime/modules"
	"SiaPrime/modules/renter/proto"
	"SiaPrime/persist"
	siasync "SiaPrime/sync"
	"SiaPrime/types"
)

var (
	errNilCS     = errors.New("cannot create contractor with nil consensus set")
	errNilTpool  = errors.New("cannot create contractor with nil transaction pool")
	errNilWallet = errors.New("cannot create contractor with nil wallet")

	// COMPATv1.0.4-lts
	// metricsContractID identifies a special contract that contains aggregate
	// financial metrics from older contractors
	metricsContractID = types.FileContractID{'m', 'e', 't', 'r', 'i', 'c', 's'}
)

// A Contractor negotiates, revises, renews, and provides access to file
// contracts.
type Contractor struct {
	// dependencies
	cs         consensusSet
	hdb        hostDB
	log        *persist.Logger
	mu         sync.RWMutex
	persist    persister
	staticDeps modules.Dependencies
	tg         siasync.ThreadGroup
	tpool      transactionPool
	wallet     wallet

	// Only one thread should be performing contract maintenance at a time.
	interruptMaintenance chan struct{}
	maintenanceLock      siasync.TryMutex

	allowance     modules.Allowance
	blockHeight   types.BlockHeight
	currentPeriod types.BlockHeight
	lastChange    modules.ConsensusChangeID

	downloaders         map[types.FileContractID]*hostDownloader
	editors             map[types.FileContractID]*hostEditor
	numFailedRenews     map[types.FileContractID]types.BlockHeight
	pubKeysToContractID map[string]types.FileContractID
	contractIDToPubKey  map[types.FileContractID]types.SiaPublicKey
	renewing            map[types.FileContractID]bool // prevent revising during renewal

	// renewedFrom links the new contract's ID to the old contract's ID
	// renewedTo links the old contract's ID to the new contract's ID
	staticContracts *proto.ContractSet
	oldContracts    map[types.FileContractID]modules.RenterContract
	renewedFrom     map[types.FileContractID]types.FileContractID
	renewedTo       map[types.FileContractID]types.FileContractID
}

// Allowance returns the current allowance.
func (c *Contractor) Allowance() modules.Allowance {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.allowance
}

// PeriodSpending returns the amount spent on contracts during the current
// billing period.
func (c *Contractor) PeriodSpending() modules.ContractorSpending {
	allContracts := c.staticContracts.ViewAll()
	c.mu.RLock()
	defer c.mu.RUnlock()

	var spending modules.ContractorSpending
	for _, contract := range allContracts {
		// Calculate ContractFees
		spending.ContractFees = spending.ContractFees.Add(contract.ContractFee)
		spending.ContractFees = spending.ContractFees.Add(contract.TxnFee)
		spending.ContractFees = spending.ContractFees.Add(contract.SiafundFee)
		// Calculate TotalAllocated
		spending.TotalAllocated = spending.TotalAllocated.Add(contract.TotalCost)
		spending.ContractSpendingDeprecated = spending.TotalAllocated
		// Calculate Spending
		spending.DownloadSpending = spending.DownloadSpending.Add(contract.DownloadSpending)
		spending.UploadSpending = spending.UploadSpending.Add(contract.UploadSpending)
		spending.StorageSpending = spending.StorageSpending.Add(contract.StorageSpending)
	}

	// Calculate needed spending to be reported from old contracts
	for _, contract := range c.oldContracts {
		host, exist := c.hdb.Host(contract.HostPublicKey)
		if contract.StartHeight >= c.currentPeriod {
			// Calculate spending from contracts that were renewed during the current period
			// Calculate ContractFees
			spending.ContractFees = spending.ContractFees.Add(contract.ContractFee)
			spending.ContractFees = spending.ContractFees.Add(contract.TxnFee)
			spending.ContractFees = spending.ContractFees.Add(contract.SiafundFee)
			// Calculate TotalAllocated
			spending.TotalAllocated = spending.TotalAllocated.Add(contract.TotalCost)
			// Calculate Spending
			spending.DownloadSpending = spending.DownloadSpending.Add(contract.DownloadSpending)
			spending.UploadSpending = spending.UploadSpending.Add(contract.UploadSpending)
			spending.StorageSpending = spending.StorageSpending.Add(contract.StorageSpending)
		} else if exist && contract.EndHeight+host.WindowSize+types.MaturityDelay > c.blockHeight {
			// Calculate funds that are being withheld in contracts
			spending.WithheldFunds = spending.WithheldFunds.Add(contract.RenterFunds)
			// Record the largest window size for worst case when reporting the spending
			if contract.EndHeight+host.WindowSize+types.MaturityDelay >= spending.ReleaseBlock {
				spending.ReleaseBlock = contract.EndHeight + host.WindowSize + types.MaturityDelay
			}
			// Calculate Previous spending
			spending.PreviousSpending = spending.PreviousSpending.Add(contract.ContractFee).Add(contract.TxnFee).
				Add(contract.SiafundFee).Add(contract.DownloadSpending).Add(contract.UploadSpending).Add(contract.StorageSpending)
		} else {
			// Calculate Previous spending
			spending.PreviousSpending = spending.PreviousSpending.Add(contract.ContractFee).Add(contract.TxnFee).
				Add(contract.SiafundFee).Add(contract.DownloadSpending).Add(contract.UploadSpending).Add(contract.StorageSpending)
		}
	}

	// Calculate amount of spent money to get unspent money.
	allSpending := spending.ContractFees
	allSpending = allSpending.Add(spending.DownloadSpending)
	allSpending = allSpending.Add(spending.UploadSpending)
	allSpending = allSpending.Add(spending.StorageSpending)
	if c.allowance.Funds.Cmp(allSpending) >= 0 {
		spending.Unspent = c.allowance.Funds.Sub(allSpending)
	}

	return spending
}

// CurrentPeriod returns the height at which the current allowance period
// began.
func (c *Contractor) CurrentPeriod() types.BlockHeight {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentPeriod
}

// RateLimits sets the bandwidth limits for connections created by the
// contractSet.
func (c *Contractor) RateLimits() (readBPW int64, writeBPS int64, packetSize uint64) {
	return c.staticContracts.RateLimits()
}

// SetRateLimits sets the bandwidth limits for connections created by the
// contractSet.
func (c *Contractor) SetRateLimits(readBPS int64, writeBPS int64, packetSize uint64) {
	c.staticContracts.SetRateLimits(readBPS, writeBPS, packetSize)
}

// Close closes the Contractor.
func (c *Contractor) Close() error {
	return c.tg.Stop()
}

// New returns a new Contractor.
func New(cs consensusSet, wallet walletShim, tpool transactionPool, hdb hostDB, persistDir string) (*Contractor, error) {
	// Check for nil inputs.
	if cs == nil {
		return nil, errNilCS
	}
	if wallet == nil {
		return nil, errNilWallet
	}
	if tpool == nil {
		return nil, errNilTpool
	}

	// Create the persist directory if it does not yet exist.
	if err := os.MkdirAll(persistDir, 0700); err != nil {
		return nil, err
	}

	// Convert the old persist file(s), if necessary. This must occur before
	// loading the contract set.
	if err := convertPersist(persistDir); err != nil {
		return nil, err
	}

	// Create the contract set.
	contractSet, err := proto.NewContractSet(filepath.Join(persistDir, "contracts"), modules.ProdDependencies)
	if err != nil {
		return nil, err
	}
	// Create the logger.
	logger, err := persist.NewFileLogger(filepath.Join(persistDir, "contractor.log"))
	if err != nil {
		return nil, err
	}

	// Create Contractor using production dependencies.
	return NewCustomContractor(cs, &WalletBridge{W: wallet}, tpool, hdb, contractSet, NewPersist(persistDir), logger, modules.ProdDependencies)
}

// NewCustomContractor creates a Contractor using the provided dependencies.
func NewCustomContractor(cs consensusSet, w wallet, tp transactionPool, hdb hostDB, contractSet *proto.ContractSet, p persister, l *persist.Logger, deps modules.Dependencies) (*Contractor, error) {
	// Create the Contractor object.
	c := &Contractor{
		cs:         cs,
		staticDeps: deps,
		hdb:        hdb,
		log:        l,
		persist:    p,
		tpool:      tp,
		wallet:     w,

		interruptMaintenance: make(chan struct{}),

		staticContracts:     contractSet,
		downloaders:         make(map[types.FileContractID]*hostDownloader),
		editors:             make(map[types.FileContractID]*hostEditor),
		oldContracts:        make(map[types.FileContractID]modules.RenterContract),
		contractIDToPubKey:  make(map[types.FileContractID]types.SiaPublicKey),
		pubKeysToContractID: make(map[string]types.FileContractID),
		renewing:            make(map[types.FileContractID]bool),
		renewedFrom:         make(map[types.FileContractID]types.FileContractID),
		renewedTo:           make(map[types.FileContractID]types.FileContractID),
	}

	// Close the contract set and logger upon shutdown.
	c.tg.AfterStop(func() {
		if err := c.staticContracts.Close(); err != nil {
			c.log.Println("Failed to close contract set:", err)
		}
		if err := c.log.Close(); err != nil {
			fmt.Println("Failed to close the contractor logger:", err)
		}
	})

	// Load the prior persistence structures.
	err := c.load()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// Subscribe to the consensus set.
	err = cs.ConsensusSetSubscribe(c, c.lastChange, c.tg.StopChan())
	if err == modules.ErrInvalidConsensusChangeID {
		// Reset the contractor consensus variables and try rescanning.
		c.blockHeight = 0
		c.lastChange = modules.ConsensusChangeBeginning
		err = cs.ConsensusSetSubscribe(c, c.lastChange, c.tg.StopChan())
	}
	if err != nil {
		return nil, errors.New("contractor subscription failed: " + err.Error())
	}
	// Unsubscribe from the consensus set upon shutdown.
	c.tg.OnStop(func() {
		cs.Unsubscribe(c)
	})

	// We may have upgraded persist or resubscribed. Save now so that we don't
	// lose our work.
	c.mu.Lock()
	err = c.save()
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}

	// Initialize the contractIDToPubKey map
	for _, contract := range c.oldContracts {
		c.contractIDToPubKey[contract.ID] = contract.HostPublicKey
		c.pubKeysToContractID[string(contract.HostPublicKey.Key)] = contract.ID
	}
	for _, contract := range c.staticContracts.ViewAll() {
		c.contractIDToPubKey[contract.ID] = contract.HostPublicKey
		c.pubKeysToContractID[string(contract.HostPublicKey.Key)] = contract.ID
	}

	// Update the allowance in the hostdb with the one that was loaded from
	// disk.
	err = c.hdb.SetAllowance(c.allowance)
	if err != nil {
		return nil, err
	}
	return c, nil
}
