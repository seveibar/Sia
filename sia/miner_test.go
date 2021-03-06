package sia

import (
	"testing"

	"github.com/NebulousLabs/Sia/consensus"
)

// mineSingleBlock mines a single block and then uses the blocking function
// processBlock to integrate the block with the state.
func mineSingleBlock(t *testing.T, c *Core) {
	b, found, err := c.miner.SolveBlock()
	for !found && err == nil {
		b, found, err = c.miner.SolveBlock()
	}
	if err != nil {
		t.Error(err)
	}
	err = c.processBlock(b)
	if err != nil && err != consensus.BlockKnownErr {
		t.Error(err)
	}
}

func testMinerDeadlocking(t *testing.T, c *Core) {
	threads := 1
	c.MinerInfo()
	c.UpdateMiner(threads)
	c.StartMining()
	c.MinerInfo()
	c.StopMining()
}
