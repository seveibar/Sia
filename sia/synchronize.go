package sia

import (
	"errors"

	"github.com/NebulousLabs/Sia/consensus"
	"github.com/NebulousLabs/Sia/network"
)

const (
	MaxCatchUpBlocks = 100
)

var moreBlocksErr = errors.New("more blocks are available")

// SendBlocks takes a list of block ids as input, and sends all blocks from
func (c *Core) SendBlocks(knownBlocks [32]consensus.BlockID) (blocks []consensus.Block, err error) {
	// Find the most recent block from knownBlocks that is in our current path.
	found := false
	var highest consensus.BlockHeight
	for _, id := range knownBlocks {
		height, err := c.state.HeightOfBlock(id)
		if err == nil {
			found = true
			if height > highest {
				highest = height
			}
		}
	}
	if !found {
		// The genesis block should be included in knownBlocks - if no matching
		// blocks are found the caller is probably on a different blockchain
		// altogether.
		err = errors.New("no matching block found")
		return
	}

	// Send over all blocks from the first known block.
	for i := highest; i < highest+MaxCatchUpBlocks; i++ {
		b, err := c.state.BlockAtHeight(i)
		if err != nil {
			break
		}
		blocks = append(blocks, b)
	}

	// If more blocks are available, send a benign error
	if _, maxErr := c.state.BlockAtHeight(highest + MaxCatchUpBlocks); maxErr == nil {
		err = moreBlocksErr
	}

	return
}

// CatchUp synchronizes with a peer to acquire any missing blocks. The
// requester sends 32 blocks, starting with the 12 most recent and then
// progressing exponentially backwards to the genesis block. The receiver uses
// these blocks to find the most recent block seen by both peers, and then
// transmits blocks sequentially until the requester is fully synchronized.
func (c *Core) CatchUp(peer network.Address) {
	knownBlocks := make([]consensus.BlockID, 0, 32)
	for i := consensus.BlockHeight(0); i < 12; i++ {
		block, badBlockErr := c.state.BlockAtHeight(c.state.Height() - i)
		if badBlockErr != nil {
			break
		}
		knownBlocks = append(knownBlocks, block.ID())
	}

	backtrace := consensus.BlockHeight(12)
	for i := 12; i < 31; i++ {
		backtrace *= 2
		block, badBlockErr := c.state.BlockAtHeight(c.state.Height() - backtrace)
		if badBlockErr != nil {
			break
		}
		knownBlocks = append(knownBlocks, block.ID())
	}
	// always include the genesis block
	genesis, _ := c.state.BlockAtHeight(0)
	knownBlocks = append(knownBlocks, genesis.ID())

	// prepare for RPC
	var newBlocks []consensus.Block
	var blockArray [32]consensus.BlockID
	copy(blockArray[:], knownBlocks)

	// unlock state during network I/O
	err := peer.RPC("SendBlocks", blockArray, &newBlocks)
	if err != nil && err.Error() != moreBlocksErr.Error() {
		// log error
		// TODO: try a different peer?
		return
	}
	for _, block := range newBlocks {
		c.AcceptBlock(block)
	}

	// TODO: There is probably a better approach than to call CatchUp
	// recursively. Furthermore, if there is a reorg that's greater than 100
	// blocks, CatchUp is going to fail outright.
	if err != nil && err.Error() == moreBlocksErr.Error() {
		go c.CatchUp(peer)
	}
}
