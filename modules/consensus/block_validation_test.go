package consensus

import (
	"testing"

	"SiaPrime/types"
)

// mockMarshaler is a mock implementation of the encoding.GenericMarshaler
// interface that allows the client to pre-define the length of the marshaled
// data.
type mockMarshaler struct {
	marshalLength uint64
}

// Marshal marshals an object into an empty byte slice of marshalLength.
func (m mockMarshaler) Marshal(interface{}) []byte {
	return make([]byte, m.marshalLength)
}

// Unmarshal is not implemented.
func (m mockMarshaler) Unmarshal([]byte, interface{}) error {
	panic("not implemented")
}

// mockClock is a mock implementation of the types.Clock interface that allows
// the client to pre-define a return value for Now().
type mockClock struct {
	now types.Timestamp
}

// Now returns mockClock's pre-defined Timestamp.
func (c mockClock) Now() types.Timestamp {
	return c.now
}

var validateBlockTests = []struct {
	now            types.Timestamp
	minTimestamp   types.Timestamp
	blockTimestamp types.Timestamp
	blockSize      uint64
	errWant        error
	msg            string
}{
	{
		minTimestamp:   types.Timestamp(5),
		blockTimestamp: types.Timestamp(4),
		errWant:        errEarlyTimestamp,
		msg:            "ValidateBlock should reject blocks with timestamps that are too early",
	},
	{
		blockSize: types.BlockSizeLimit + 1,
		errWant:   errLargeBlock,
		msg:       "ValidateBlock should reject excessively large blocks",
	},
	{
		now:            types.Timestamp(50),
		blockTimestamp: types.Timestamp(50) + types.ExtremeFutureThreshold + 1,
		errWant:        errExtremeFutureTimestamp,
		msg:            "ValidateBlock should reject blocks timestamped in the extreme future",
	},
}

// TestUnitValidateBlock runs a series of unit tests for ValidateBlock.
func TestUnitValidateBlock(t *testing.T) {
	// TODO(mtlynch): Populate all parameters to ValidateBlock so that everything
	// is valid except for the attribute that causes validation to fail. (i.e.
	// don't assume an ordering to the implementation of the validation function).
	for _, tt := range validateBlockTests {
		b := types.Block{
			Timestamp: tt.blockTimestamp,
		}
		blockValidator := stdBlockValidator{
			marshaler: mockMarshaler{
				marshalLength: tt.blockSize,
			},
			clock: mockClock{
				now: tt.now,
			},
		}
		err := blockValidator.ValidateBlock(b, b.ID(), tt.minTimestamp, types.RootDepth, 0, nil)
		if err != tt.errWant {
			t.Errorf("%s: got %v, want %v", tt.msg, err, tt.errWant)
		}
	}
}

// TestCheckMinerPayoutsWithoutDevFee probes the checkMinerPayouts function.
func TestCheckMinerPayoutsWithoutDevFee(t *testing.T) {
	// All tests are done at height = 0
	height := types.BlockHeight(0)
	coinbase := types.CalculateCoinbase(height)
	devFundEnabled := types.DevFundEnabled
	devFundInitialBlockHeight := types.DevFundInitialBlockHeight
	devFundDecayStartBlockHeight := uint64(types.DevFundDecayStartBlockHeight)
	devFundDecayEndBlockHeight := uint64(types.DevFundDecayEndBlockHeight)
	devFundInitialPercentage := types.DevFundInitialPercentage
	devFundFinalPercentage := types.DevFundFinalPercentage
	devFundPercentageRange := devFundInitialPercentage - devFundFinalPercentage
	devFundDecayPercentage := uint64(100)
	if uint64(height) >= devFundDecayEndBlockHeight {
		devFundDecayPercentage = uint64(0)
	} else if uint64(height) >= devFundDecayStartBlockHeight {
		devFundDecayPercentage = uint64(100) - (uint64(height)-devFundDecayStartBlockHeight)*uint64(100)/(devFundDecayEndBlockHeight-devFundDecayStartBlockHeight)
	}
	devFundPercentage := devFundFinalPercentage*uint64(100) + devFundPercentageRange*devFundDecayPercentage
	devSubsidy := coinbase.MulFloat(0)
	if devFundEnabled && height >= devFundInitialBlockHeight {
		devSubsidy = coinbase.Mul(types.NewCurrency64(devFundPercentage).Div(types.NewCurrency64(10000)))
	}
	minerSubsidy := coinbase.Sub(devSubsidy)

	// Create a block with a single coinbase payout, and no dev fund payout.
	b := types.Block{
		MinerPayouts: []types.SiacoinOutput{
			{Value: minerSubsidy},
		},
	}
	if !checkMinerPayouts(b, 0) {
		t.Error("payouts evaluated incorrectly when there is only one payout.")
	}

	// Try a block with an incorrect payout.
	b = types.Block{
		MinerPayouts: []types.SiacoinOutput{
			{Value: minerSubsidy.Sub(types.NewCurrency64(1))},
		},
	}
	if checkMinerPayouts(b, 0) {
		t.Error("payouts evaluated incorrectly when there is a too-small payout")
	}

	// Try a block with 2 payouts.
	b = types.Block{
		MinerPayouts: []types.SiacoinOutput{
			{Value: minerSubsidy.Sub(types.NewCurrency64(1))},
			{Value: types.NewCurrency64(1)},
		},
	}
	if !checkMinerPayouts(b, 0) {
		t.Error("payouts evaluated incorrectly when there are 2 payouts")
	}

	// Try a block with 2 payouts that are too large.
	b = types.Block{
		MinerPayouts: []types.SiacoinOutput{
			{Value: minerSubsidy},
			{Value: minerSubsidy},
		},
	}
	if checkMinerPayouts(b, 0) {
		t.Error("payouts evaluated incorrectly when there are two large payouts")
	}

	// Create a block with an empty payout.
	b = types.Block{
		MinerPayouts: []types.SiacoinOutput{
			{Value: minerSubsidy},
			{},
		},
	}
	if checkMinerPayouts(b, 0) {
		t.Error("payouts evaluated incorrectly when there is only one payout.")
	}
}

// TestCheckMinerPayoutsWithDevFee probes the checkMinerPayouts function.
func TestCheckMinerPayoutsWithDevFee(t *testing.T) {
	// All tests are done at height = 1.
	height := types.BlockHeight(80000)
	coinbase := types.CalculateCoinbase(height)
	devFundEnabled := types.DevFundEnabled
	devFundInitialBlockHeight := types.DevFundInitialBlockHeight
	devFundDecayStartBlockHeight := uint64(types.DevFundDecayStartBlockHeight)
	devFundDecayEndBlockHeight := uint64(types.DevFundDecayEndBlockHeight)
	devFundInitialPercentage := types.DevFundInitialPercentage
	devFundFinalPercentage := types.DevFundFinalPercentage
	devFundPercentageRange := devFundInitialPercentage - devFundFinalPercentage
	devFundDecayPercentage := uint64(100)
	if uint64(height) >= devFundDecayEndBlockHeight {
		devFundDecayPercentage = uint64(0)
	} else if uint64(height) >= devFundDecayStartBlockHeight {
		devFundDecayPercentage = uint64(100) - (uint64(height)-devFundDecayStartBlockHeight)*uint64(100)/(devFundDecayEndBlockHeight-devFundDecayStartBlockHeight)
	}
	devFundPercentage := devFundFinalPercentage*uint64(100) + devFundPercentageRange*devFundDecayPercentage
	devSubsidy := coinbase.MulFloat(0)
	if devFundEnabled && height >= devFundInitialBlockHeight {
		devSubsidy = coinbase.Mul(types.NewCurrency64(devFundPercentage)).Div(types.NewCurrency64(uint64(10000)))
	}
	minerSubsidy := coinbase.Sub(devSubsidy)

	// Create a block with a single coinbase payout, and no dev fund payout.
	b := types.Block{
		MinerPayouts: []types.SiacoinOutput{
			{Value: coinbase},
		},
	}
	if devFundEnabled && checkMinerPayouts(b, height) {
		t.Error("payouts evaluated incorrectly when the dev fund is enabled and there is a coinbase payout but not a dev fund payout.")
	}
	if !devFundEnabled && !checkMinerPayouts(b, height) {
		t.Error("payouts evaluated incorrectly when the dev fund is disabled and there is a coinbase payout and a dev fund payout.")
	}
	// Create a block with a valid miner payout, and a dev fund payout with no unlock hash.
	b = types.Block{
		MinerPayouts: []types.SiacoinOutput{
			{Value: minerSubsidy},
			{Value: devSubsidy},
		},
	}
	if checkMinerPayouts(b, height) {
		t.Error("payouts evaluated incorrectly when we are missing the dev fund unlock hash.")
	}
	// Create a block with a valid miner payout, and a dev fund payout with an incorrect unlock hash.
	b = types.Block{
		MinerPayouts: []types.SiacoinOutput{
			{Value: minerSubsidy},
			{Value: devSubsidy, UnlockHash: types.UnlockHash{0, 1}},
		},
	}
	if checkMinerPayouts(b, height) {
		t.Error("payouts evaluated incorrectly when we have an incorrect dev fund unlock hash.")
	}
	// Create a block with a valid miner payout, but no dev fund payout.
	b = types.Block{
		MinerPayouts: []types.SiacoinOutput{
			{Value: minerSubsidy},
		},
	}
	if devFundEnabled && checkMinerPayouts(b, height) {
		t.Error("payouts evaluated incorrectly when the dev fund is enabled and we are missing the dev fund payout but have a proper miner payout.")
	}
	if !devFundEnabled && !checkMinerPayouts(b, height) {
		t.Error("payouts evaluated incorrectly when the dev fund is disabled and we have a proper miner payout.")
	}
	// Create a block with a valid dev fund payout, but no miner payout.
	b = types.Block{
		MinerPayouts: []types.SiacoinOutput{
			{Value: devSubsidy, UnlockHash: types.DevFundUnlockHash},
		},
	}
	if checkMinerPayouts(b, height) {
		t.Error("payouts evaluated incorrectly when we are missing the miner payout but have a proper dev fund payout.")
	}
	// Create a block with a valid miner payout and a valid dev fund payout.
	b = types.Block{
		MinerPayouts: []types.SiacoinOutput{
			{Value: minerSubsidy},
			{Value: devSubsidy, UnlockHash: types.DevFundUnlockHash},
		},
	}
	if devFundEnabled && !checkMinerPayouts(b, height) {
		t.Error("payouts evaluated incorrectly when there are only two payouts and the dev fund is enabled.")
	}
	if !devFundEnabled && checkMinerPayouts(b, height) {
		t.Error("payouts evaluated incorrectly when there are only two payouts and the dev fund is disabled.")
	}

	// Try a block with an incorrect payout.
	b = types.Block{
		MinerPayouts: []types.SiacoinOutput{
			{Value: coinbase.Sub(types.NewCurrency64(1))},
		},
	}
	if checkMinerPayouts(b, height) {
		t.Error("payouts evaluated incorrectly when there is a too-small payout")
	}

	minerPayout := coinbase.Sub(devSubsidy).Sub(types.NewCurrency64(1))
	secondMinerPayout := types.NewCurrency64(1)
	// Try a block with 3 payouts.
	b = types.Block{
		MinerPayouts: []types.SiacoinOutput{
			{Value: minerPayout},
			{Value: secondMinerPayout},
			{Value: devSubsidy, UnlockHash: types.DevFundUnlockHash},
		},
	}
	if devFundEnabled && !checkMinerPayouts(b, height) {
		t.Error("payouts evaluated incorrectly when there are 3 payouts and the dev fund is enabled.")
	}
	if !devFundEnabled && checkMinerPayouts(b, height) {
		t.Error("payouts evaluated incorrectly when there are 3 payouts and the dev fund is disabled.")
	}

	// Try a block with 2 payouts that are too large.
	b = types.Block{
		MinerPayouts: []types.SiacoinOutput{
			{Value: coinbase},
			{Value: coinbase},
		},
	}
	if checkMinerPayouts(b, height) {
		t.Error("payouts evaluated incorrectly when there are two large payouts")
	}

	// Create a block with an empty payout.
	b = types.Block{
		MinerPayouts: []types.SiacoinOutput{
			{Value: coinbase},
			{},
		},
	}
	if checkMinerPayouts(b, height) {
		t.Error("payouts evaluated incorrectly when there is only one payout.")
	}
}

// TestCheckTarget probes the checkTarget function.
func TestCheckTarget(t *testing.T) {
	var b types.Block
	lowTarget := types.RootDepth
	highTarget := types.Target{}
	sameTarget := types.Target(b.ID())

	if !checkTarget(b, b.ID(), lowTarget) {
		t.Error("CheckTarget failed for a low target")
	}
	if checkTarget(b, b.ID(), highTarget) {
		t.Error("CheckTarget passed for a high target")
	}
	if !checkTarget(b, b.ID(), sameTarget) {
		t.Error("CheckTarget failed for a same target")
	}
}
