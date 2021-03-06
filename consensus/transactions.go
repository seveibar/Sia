package consensus

import (
	"errors"
	"fmt"

	"github.com/NebulousLabs/Sia/crypto"
)

// Exported errors
var (
	ConflictingTransactionErr = errors.New("conflicting transaction exists in transaction pool")
	InvalidSignatureErr       = errors.New("invalid signature in transaction")
)

// Each input has a list of public keys and a required number of signatures.
// InputSignatures keeps track of which public keys have been used and how many
// more signatures are needed.
type InputSignatures struct {
	RemainingSignatures uint64
	PossibleKeys        []crypto.PublicKey
	UsedKeys            map[uint64]struct{}
	Index               int
}

// reverseTransaction removes a given transaction from the
// ConsensusState, making it as though the transaction had never happened.
func (s *State) invertTransaction(t Transaction) (diffs []OutputDiff) {
	// Delete all the open contracts created by new contracts.
	for i := range t.FileContracts {
		contractID := t.FileContractID(i)
		delete(s.openContracts, contractID)
	}

	// Delete all outputs created by storage proofs.
	for _, sp := range t.StorageProofs {
		diffs = append(diffs, s.invertStorageProof(sp))
	}

	// Delete all financial outputs created by the transaction.
	for i := range t.Outputs {
		output, err := s.output(t.OutputID(i))
		if err != nil {
			panic(err)
		}
		diff := OutputDiff{New: false, ID: t.OutputID(i), Output: output}
		delete(s.unspentOutputs, t.OutputID(i))
		diffs = append(diffs, diff)
	}

	// Restore all inputs to the unspent outputs list.
	for _, input := range t.Inputs {
		diff := OutputDiff{New: true, ID: input.OutputID, Output: s.spentOutputs[input.OutputID]}
		s.unspentOutputs[input.OutputID] = s.spentOutputs[input.OutputID]
		diffs = append(diffs, diff)
		delete(s.spentOutputs, input.OutputID)
	}

	// Add the transaction to the transaction pool.
	s.addTransactionToPool(&t)
	return
}

// applyTransaction() takes a transaction and adds it to the
// ConsensusState, updating the list of contracts, outputs, etc.
func (s *State) applyTransaction(t Transaction) (diffs TransactionDiff) {
	// Update the transaction pool to resolve any conflicts.
	s.removeTransactionConflictsFromPool(&t)

	// Remove all inputs from the unspent outputs list.
	for _, input := range t.Inputs {
		// Sanity check.
		if DEBUG {
			_, exists := s.unspentOutputs[input.OutputID]
			if !exists {
				panic("Applying a transaction with an invalid unspent output!")
			}
		}

		s.spentOutputs[input.OutputID] = s.unspentOutputs[input.OutputID]
		diff := OutputDiff{New: false, ID: input.OutputID, Output: s.unspentOutputs[input.OutputID]}
		delete(s.unspentOutputs, input.OutputID)
		diffs.OutputDiffs = append(diffs.OutputDiffs, diff)
	}

	// Add all finanacial outputs to the unspent outputs list.
	for i, output := range t.Outputs {
		diff := OutputDiff{New: true, ID: t.OutputID(i), Output: output}
		s.unspentOutputs[t.OutputID(i)] = output
		diffs.OutputDiffs = append(diffs.OutputDiffs, diff)
	}

	// Add all outputs created by storage proofs.
	for _, sp := range t.StorageProofs {
		s.applyStorageProof(sp, &diffs)
	}

	// Add all new contracts to the OpenContracts list.
	for i, contract := range t.FileContracts {
		// Diff not needed here, because applying a contract doesn't change the
		// outputs set, a contract is merely being created.
		s.applyContract(contract, t.FileContractID(i), &diffs)
	}
	return
}

// validInput returns err = nil if the input is valid within the current state,
// otherwise returns an error explaining what wasn't valid.
func (s *State) validInput(input Input) (err error) {
	// Check the input spends an existing and valid output.
	_, exists := s.unspentOutputs[input.OutputID]
	if !exists {
		err = errors.New("transaction spends a nonexisting output")
		return
	}

	// Check that the spend conditions match the hash listed in the output.
	if input.SpendConditions.CoinAddress() != s.unspentOutputs[input.OutputID].SpendHash {
		err = errors.New("spend conditions do not match hash")
		return
	}

	// Check the timelock on the spend conditions is expired.
	if input.SpendConditions.TimeLock > s.height() {
		err = errors.New("output spent before timelock expiry.")
		return
	}

	return
}

// ValidTransaction returns err = nil if the transaction is valid, otherwise
// returns an error explaining what wasn't valid.
func (s *State) validTransaction(t Transaction) (err error) {
	// Iterate through each input, summing the value, checking for
	// correctness, and creating an InputSignatures object.
	inputSum := Currency(0)
	inputSignaturesMap := make(map[OutputID]*InputSignatures)
	for i, input := range t.Inputs {
		// Check that the input is valid.
		err = s.validInput(input)
		if err != nil {
			return
		}

		// Create the condition for the input signatures and add it to the input signatures map.
		_, exists := inputSignaturesMap[input.OutputID]
		if exists {
			err = errors.New("output spent twice in same transaction")
			return
		}
		newInputSignatures := &InputSignatures{
			RemainingSignatures: input.SpendConditions.NumSignatures,
			PossibleKeys:        input.SpendConditions.PublicKeys,
			Index:               i,
		}
		inputSignaturesMap[input.OutputID] = newInputSignatures

		// Add the input value to the coin sum.
		inputSum += s.unspentOutputs[input.OutputID].Value
	}

	// Tally up the miner fees and output values.
	outputSum := Currency(0)
	for _, minerFee := range t.MinerFees {
		outputSum += minerFee
	}
	for _, output := range t.Outputs {
		outputSum += output.Value
	}

	// Verify the contracts and tally up the expenditures.
	for _, contract := range t.FileContracts {
		err = s.validContract(contract)
		if err != nil {
			return
		}

		outputSum += contract.ContractFund
	}

	// Check that all provided proofs are valid.
	for _, proof := range t.StorageProofs {
		err = s.validProof(proof)
		if err != nil {
			return
		}
	}

	// Check that the outputs are less than or equal to the outputs.
	if inputSum < outputSum {
		errorString := fmt.Sprintf("Inputs do not equal outputs for transaction: inputs=%v : outputs=%v", inputSum, outputSum)
		for _, input := range t.Inputs {
			errorString += fmt.Sprintf("\nInput: %v", s.unspentOutputs[input.OutputID].Value)
		}
		for _, fee := range t.MinerFees {
			errorString += fmt.Sprintf("\nMiner Fee: %v", fee)
		}
		for _, output := range t.Outputs {
			errorString += fmt.Sprintf("\nOutput: %v", output.Value)
		}
		for _, fc := range t.FileContracts {
			errorString += fmt.Sprintf("\nContract Fund: %v", fc.ContractFund)
		}
		err = errors.New(errorString)
		return
	}

	// Check all of the signatures for validity.
	for i, sig := range t.Signatures {
		// Check that each signature signs a unique pubkey where
		// RemainingSignatures > 0.
		if inputSignaturesMap[sig.InputID].RemainingSignatures == 0 {
			err = errors.New("friviolous signature detected.")
			return
		}
		_, exists := inputSignaturesMap[sig.InputID].UsedKeys[sig.PublicKeyIndex]
		if exists {
			err = errors.New("public key used twice while signing")
			return
		}

		// Check the timelock on the signature.
		if sig.TimeLock > s.height() {
			err = errors.New("signature timelock has not expired")
			return
		}

		// Check that the signature matches the public key.
		sigHash := t.SigHash(i)
		if !crypto.VerifyBytes(sigHash[:], inputSignaturesMap[sig.InputID].PossibleKeys[sig.PublicKeyIndex], sig.Signature) {
			err = InvalidSignatureErr
			return
		}

		// Subtract the number of signatures remaining in the InputSignatures field.
		inputSignaturesMap[sig.InputID].RemainingSignatures -= 1
	}

	// Check that all inputs have been signed by sufficient public keys.
	for _, inputSignatures := range inputSignaturesMap {
		if inputSignatures.RemainingSignatures != 0 {
			err = fmt.Errorf("an input has not been fully signed: %v", inputSignatures.Index)
			return
		}
	}

	return
}

func (s *State) ValidTransaction(t Transaction) (err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.validTransaction(t)
}

// State.AcceptTransaction() checks for a conflict of the transaction with the
// transaction pool, then checks that the transaction is valid given the
// current state, then adds the transaction to the transaction pool.
// AcceptTransaction() is thread safe, and can be called concurrently.
func (s *State) AcceptTransaction(t Transaction) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check that the transaction is not in conflict with the transaction
	// pool.
	if s.transactionPoolConflict(&t) {
		err = ConflictingTransactionErr
		return
	}

	// Check that the transaction is potentially valid.
	err = s.validTransaction(t)
	if err != nil {
		return
	}

	// Add the transaction to the pool.
	s.addTransactionToPool(&t)

	return
}
