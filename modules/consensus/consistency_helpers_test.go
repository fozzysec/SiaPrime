package consensus

import (
	"SiaPrime/crypto"

	"gitlab.com/NebulousLabs/bolt"
)

// dbConsensusChecksum is a convenience function to call consensusChecksum
// without a bolt.Tx.
func (cs *ConsensusSet) dbConsensusChecksum() (checksum crypto.Hash) {
	err := cs.db.Update(func(tx *bolt.Tx) error {
		checksum = consensusChecksum(tx)
		return nil
	})
	if err != nil {
		panic(err)
	}
	return checksum
}
