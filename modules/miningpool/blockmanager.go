package pool

import (
	"errors"
	"time"

	"SiaPrime/modules"
	"SiaPrime/types"
)

var (
	errLateHeader = errors.New("header is old, block could not be recovered")
)

func (p *Pool) blockForWorkWithoutDevFund() types.Block {
	p.persist.mu.Lock()
	defer p.persist.mu.Unlock()

	b := p.sourceBlock
	b.Transactions = p.blockTxns.transactions()

	// Update the timestamp.
	if b.Timestamp < types.CurrentTimestamp() {
		b.Timestamp = types.CurrentTimestamp()
	}

	payoutVal := b.CalculateSubsidy(p.persist.GetBlockHeight() + 1)
	p.log.Printf("building a new source block, block id is: %s\n", b.ID())
	p.log.Printf("miner fees cost: %s", b.CalculateMinerFees().String())
	p.log.Printf("# transactions: %d", len(b.Transactions))
	p.log.Printf("payout value is: %s", payoutVal.String())
	b.MinerPayouts = []types.SiacoinOutput{{
		Value:      payoutVal,
		UnlockHash: p.persist.Settings.PoolWallet,
	}}

	return b
}

func (p *Pool) blockForWorkWithDevFund() types.Block {
	p.persist.mu.Lock()
	defer p.persist.mu.Unlock()

	b := p.sourceBlock
	b.Transactions = p.blockTxns.transactions()

	// Update the timestamp.
	if b.Timestamp < types.CurrentTimestamp() {
		b.Timestamp = types.CurrentTimestamp()
	}

	minerPayoutVal, subsidyPayoutVal := b.CalculateSubsidies(p.persist.GetBlockHeight() + 1)
	subsidyUnlockHash := types.DevFundUnlockHash
	if types.BurnAddressBlockHeight != types.BlockHeight(0) && (p.persist.GetBlockHeight()+1) >= types.BurnAddressBlockHeight {
		subsidyUnlockHash = types.BurnAddressUnlockHash
	}
	p.log.Printf("building a new source block, block id is: %s\n", b.ID())
	p.log.Printf("miner fees cost: %s", b.CalculateMinerFees().String())
	p.log.Printf("# transactions: %d", len(b.Transactions))
	p.log.Printf("payout value is: %s", minerPayoutVal.String())
	b.MinerPayouts = []types.SiacoinOutput{{
		Value:      minerPayoutVal,
		UnlockHash: p.persist.Settings.PoolWallet,
	}, {
		Value:      subsidyPayoutVal,
		UnlockHash: subsidyUnlockHash,
	}}

	return b
}

// blockForWork returns a block that is ready for nonce grinding, including
// correct miner payouts.
func (p *Pool) blockForWork() types.Block {
	if types.DevFundEnabled && p.persist.GetBlockHeight()+1 >= types.DevFundInitialBlockHeight {
		return p.blockForWorkWithDevFund()
	}
	return p.blockForWorkWithoutDevFund()
}

// newSourceBlock creates a new source block for the block manager so that new
// headers will use the updated source block.
func (p *Pool) newSourceBlock() {
	// To guarantee garbage collection of old blocks, delete all header entries
	// that have not been reached for the current block.
	for p.memProgress%(HeaderMemory/BlockMemory) != 0 {
		delete(p.blockMem, p.headerMem[p.memProgress])
		delete(p.arbDataMem, p.headerMem[p.memProgress])
		p.memProgress++
		if p.memProgress == HeaderMemory {
			p.memProgress = 0
		}
	}

	// Update the source block.
	block := p.blockForWork()
	p.saveSync()
	p.sourceBlock = block
	p.sourceBlockTime = time.Now()
}

// managedSubmitBlock takes a solved block and submits it to the blockchain.
func (p *Pool) managedSubmitBlock(b types.Block) error {
	p.log.Printf("managedSubmitBlock called on block id: %s, block has %d txs\n", b.ID(), len(b.Transactions))
	// Give the block to the consensus set.
	err := p.cs.AcceptBlock(b)
	// Add the miner to the blocks list if the only problem is that it's stale.
	if err == modules.ErrNonExtendingBlock {
		// p.log.Debugf("Waiting to lock pool\n")
		p.mu.Lock()
		p.persist.SetBlocksFound(append(p.persist.GetBlocksFound(), b.ID()))
		// p.log.Debugf("Unlocking pool\n")
		p.mu.Unlock()
		p.log.Println("Mined a stale block - block appears valid but does not extend the blockchain")
		return err
	}
	if err == modules.ErrBlockUnsolved {
		// p.log.Println("Mined an unsolved block - header submission appears to be incorrect")
		return err
	}
	if err != nil {
		p.tpool.PurgeTransactionPool()
		p.log.Println("ERROR: an invalid block was submitted:", err)
		return err
	}
	// p.log.Debugf("Waiting to lock pool\n")
	p.mu.Lock()
	defer p.mu.Unlock()

	// Grab a new address for the miner. Call may fail if the wallet is locked
	// or if the wallet addresses have been exhausted.
	p.persist.SetBlocksFound(append(p.persist.GetBlocksFound(), b.ID()))
	// var uc types.UnlockConditions
	// uc, err = p.wallet.NextAddress()
	// if err != nil {
	// 	return err
	// }
	// p.persist.Address = uc.UnlockHash()
	return p.saveSync()
}
