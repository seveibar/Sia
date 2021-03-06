package consensus

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"

	"github.com/NebulousLabs/Sia/hash"
)

type (
	BlockWeight *big.Rat
)

// The state struct contains a list of all known blocks, sorted into a tree
// according to the shape of the network. It also contains the
// 'ConsensusState', which represents the state of consensus on the current
// longest fork.
//
// The state has a RWMutex. Any time you read from or write to the State
// struct, you need to either have a read lock or a write lock on the state.
// Internally, the state has no concurrency, so the mutex is never used within
// the consensus package.
//
// TODO: The mutexing in the consensus package breaks convention.
//
// TODO: When applying blocks and transactions, make the state changes in real
// time (?) and then if DEBUG, remove and reapply the diffs and make sure that
// the resulting state is identical to the one that was created when applying
// in real time.
type State struct {
	// The block root operates like a linked list of blocks, forming the
	// blocktree.
	blockRoot *BlockNode

	// Missing parents is a double map, the first a map of missing parents, and
	// the second is a map of the known children to the parent. The first is
	// necessary so that if a parent is found, all the children can be added to
	// the parent. The second is necessary for checking if a new block is a
	// known orphan.
	badBlocks      map[BlockID]struct{}          // A list of blocks that don't verify.
	blockMap       map[BlockID]*BlockNode        // A list of all blocks in the blocktree.
	missingParents map[BlockID]map[BlockID]Block // A list of all missing parents and their known children.

	// The transaction pool works by storing a list of outputs that are
	// spent by transactions in the pool, and pointing to the transaction
	// that spends them. That makes it really easy to look up conflicts as
	// new transacitons arrive, and also easy to remove transactions from
	// the pool (delete every input used in the transaction.) The
	// transaction list contains only the first output, so that when
	// building blocks you can more easily iterate through every
	// transaction.
	transactionPoolOutputs map[OutputID]*Transaction
	transactionPoolProofs  map[ContractID]*Transaction
	transactionList        map[OutputID]*Transaction

	// Consensus Variables - the current state of consensus according to the
	// longest fork.
	currentBlockID BlockID
	currentPath    map[BlockHeight]BlockID // Points to the block id for a given height.
	unspentOutputs map[OutputID]Output
	openContracts  map[ContractID]*OpenContract // TODO: This probably shouldn't be a pointer.
	spentOutputs   map[OutputID]Output          // Useful for remembering how many coins an input had. TODO: This should be available in the diffs, not here.

	// consensusSubscriptions is a list of channels that receive notifications
	// each time the state of consensus changes. Consensus changes only happen
	// through the application and inversion of blocks. See notifications.go
	// for more information.
	consensusSubscriptions []chan ConsensusChange

	mu sync.RWMutex
}

// An open contract contains all information necessary to properly enforce a
// contract with no knowledge of the history of the contract.
type OpenContract struct {
	FileContract    FileContract
	ContractID      ContractID
	FundsRemaining  Currency
	Failures        uint64
	WindowSatisfied bool
}

// A missed storage proof indicates which contract missed the proof, and which
// output resulted from the missed proof. This is necessary because missed
// proofs are passive - they happen in the absense of a transaction, not in the
// presense of one. They must be stored in the block nodes so that a block can
// be correctly rewound without needing to scroll through the past
// 'ChallengeWindow' blocks to figure out if a proof was missed or not.
type MissedStorageProof struct {
	OutputID   OutputID
	ContractID ContractID
}

// CreateGenesisState will create the state that contains the genesis block and
// nothing else.
func CreateGenesisState() (s *State, diffs []OutputDiff) {
	// Create a new state and initialize the maps.
	s = &State{
		blockRoot:              new(BlockNode),
		badBlocks:              make(map[BlockID]struct{}),
		blockMap:               make(map[BlockID]*BlockNode),
		missingParents:         make(map[BlockID]map[BlockID]Block),
		currentPath:            make(map[BlockHeight]BlockID),
		openContracts:          make(map[ContractID]*OpenContract),
		unspentOutputs:         make(map[OutputID]Output),
		spentOutputs:           make(map[OutputID]Output),
		transactionPoolOutputs: make(map[OutputID]*Transaction),
		transactionPoolProofs:  make(map[ContractID]*Transaction),
		transactionList:        make(map[OutputID]*Transaction),
	}

	// Create the genesis block and add it as the BlockRoot.
	genesisBlock := Block{
		Timestamp:    GenesisTimestamp,
		MinerAddress: GenesisAddress,
	}
	s.blockRoot.Block = genesisBlock
	s.blockRoot.Height = 0
	for i := range s.blockRoot.RecentTimestamps {
		s.blockRoot.RecentTimestamps[i] = GenesisTimestamp
	}
	s.blockRoot.Target = RootTarget
	s.blockRoot.Depth = RootDepth
	s.blockMap[genesisBlock.ID()] = s.blockRoot

	// Fill out the consensus informaiton for the genesis block.
	s.currentBlockID = genesisBlock.ID()
	s.currentPath[BlockHeight(0)] = genesisBlock.ID()

	// Create the genesis subsidy output.
	genesisSubsidyOutput := Output{
		Value:     CalculateCoinbase(0),
		SpendHash: GenesisAddress,
	}
	s.unspentOutputs[genesisBlock.SubsidyID()] = genesisSubsidyOutput

	// Create the output diff for genesis subsidy.
	diff := OutputDiff{
		New:    true,
		ID:     genesisBlock.SubsidyID(),
		Output: genesisSubsidyOutput,
	}
	diffs = append(diffs, diff)

	return
}

func (s *State) height() BlockHeight {
	return s.blockMap[s.currentBlockID].Height
}

// State.Height() returns the height of the longest fork.
func (s *State) Height() BlockHeight {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.height()
}

// depth returns the depth of the current block of the state.
func (s *State) depth() Target {
	return s.currentBlockNode().Depth
}

// Depth returns the depth of the current block of the state.
func (s *State) Depth() Target {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.depth()
}

// BlockAtHeight() returns the block from the current history at the
// input height.
func (s *State) blockAtHeight(height BlockHeight) (b Block, err error) {
	if bn, ok := s.blockMap[s.currentPath[height]]; ok {
		b = bn.Block
		return
	}
	err = fmt.Errorf("no block at height %v found.", height)
	return
}

func (s *State) BlockAtHeight(height BlockHeight) (b Block, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.blockAtHeight(height)
}

// BlockFromID returns the block associated with a given id. This function
// isn't actually used anywhere right now but it seems like it might be useful
// so I'm keeping it around.
func (s *State) BlockFromID(bid BlockID) (b Block, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	node := s.blockMap[bid]
	if node == nil {
		err = errors.New("no block of that id found")
		return
	}
	b = node.Block
	return
}

// HeightOfBlock returns the height of a block given the id.
func (s *State) HeightOfBlock(bid BlockID) (height BlockHeight, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	node := s.blockMap[bid]
	if node == nil {
		err = errors.New("no block of that id found")
		return
	}
	height = node.Height
	return
}

// currentBlockNode returns the node of the most recent block in the
// longest fork.
func (s *State) currentBlockNode() *BlockNode {
	return s.blockMap[s.currentBlockID]
}

func (s *State) currentBlock() Block {
	return s.blockMap[s.currentBlockID].Block
}

// CurrentBlock returns the most recent block in the longest fork.
func (s *State) CurrentBlock() Block {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentBlock()
}

// CurrentBlockWeight() returns the weight of the current block in the
// heaviest fork.
func (s *State) currentBlockWeight() BlockWeight {
	return s.currentBlockNode().Target.Inverse()
}

func (s *State) CurrentBlockWeight() BlockWeight {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentBlockWeight()
}

// EarliestLegalTimestamp returns the earliest legal timestamp of the next
// block - earlier timestamps will render the block invalid.
func (s *State) EarliestTimestamp() Timestamp {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentBlockNode().earliestChildTimestamp()
}

// CurrentTarget returns the target of the next block that needs to be
// submitted to the state.
func (s *State) CurrentTarget() Target {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentBlockNode().Target
}

// State.Output returns the Output associated with the id provided for input,
// but only if the output is a part of the utxo set.
func (s *State) output(id OutputID) (output Output, err error) {
	output, exists := s.unspentOutputs[id]
	if exists {
		return
	}

	err = errors.New("output not in utxo set")
	return
}

func (s *State) Output(id OutputID) (output Output, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.output(id)
}

// Sorted UtxoSet returns all of the unspent transaction outputs sorted
// according to the numerical value of their id.
func (s *State) sortedUtxoSet() (sortedOutputs []Output) {
	var unspentOutputStrings []string
	for outputID := range s.unspentOutputs {
		unspentOutputStrings = append(unspentOutputStrings, string(outputID[:]))
	}
	sort.Strings(unspentOutputStrings)

	for _, utxoString := range unspentOutputStrings {
		var outputID OutputID
		copy(outputID[:], utxoString)
		output, err := s.output(outputID)
		if err != nil {
			panic(err)
		}
		sortedOutputs = append(sortedOutputs, output)
	}
	return
}

func (s *State) SortedUtxoSet() (sortedOutputs []Output) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sortedUtxoSet()
}

// StateHash returns the markle root of the current state of consensus.
func (s *State) stateHash() hash.Hash {
	// Items of interest:
	// 1. CurrentBlockID
	// 2. Current Height
	// 3. Current Target
	// 4. Current Depth
	// 5. Earliest Allowed Timestamp of Next Block
	// 6. Genesis Block
	// 7. CurrentPath, ordered by height.
	// 8. UnspentOutputs, sorted by id.
	// 9. OpenContracts, sorted by id.

	// Create a slice of hashes representing all items of interest.
	leaves := []hash.Hash{
		hash.Hash(s.currentBlockID),
		hash.HashObject(s.height()),
		hash.HashObject(s.currentBlockNode().Target),
		hash.HashObject(s.currentBlockNode().Depth),
		hash.HashObject(s.currentBlockNode().earliestChildTimestamp()),
		hash.Hash(s.blockRoot.Block.ID()),
	}

	// Add all the blocks in the current path.
	for i := 0; i < len(s.currentPath); i++ {
		leaves = append(leaves, hash.Hash(s.currentPath[BlockHeight(i)]))
	}

	// Sort the unspent outputs by the string value of their ID.
	sortedUtxos := s.sortedUtxoSet()

	// Add the unspent outputs in sorted order.
	for _, output := range sortedUtxos {
		leaves = append(leaves, hash.HashObject(output))
	}

	// Sort the open contracts by the string value of their ID.
	var openContractStrings []string
	for contractID := range s.openContracts {
		openContractStrings = append(openContractStrings, string(contractID[:]))
	}
	sort.Strings(openContractStrings)

	// Add the open contracts in sorted order.
	for _, stringContractID := range openContractStrings {
		var contractID ContractID
		copy(contractID[:], stringContractID)
		leaves = append(leaves, hash.HashObject(s.openContracts[contractID]))
	}

	return hash.MerkleRoot(leaves)
}

func (s *State) StateHash() hash.Hash {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stateHash()
}
