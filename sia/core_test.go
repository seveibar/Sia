package sia

import (
	"math/big"
	"testing"

	"github.com/NebulousLabs/Sia/consensus"
	"github.com/NebulousLabs/Sia/network"
	"github.com/NebulousLabs/Sia/sia/host"
	"github.com/NebulousLabs/Sia/sia/hostdb"
	"github.com/NebulousLabs/Sia/sia/miner"
	"github.com/NebulousLabs/Sia/sia/renter"
	"github.com/NebulousLabs/Sia/sia/wallet"
)

// establishTestingEnvrionment sets all of the testEnv variables.
func establishTestingEnvironment(t *testing.T) (c *Core) {
	// Alter the constants to create a system more friendly to testing.
	//
	// TODO: Perhaps also have these constants as a build flag, then they don't
	// need to be variables.
	consensus.BlockFrequency = consensus.Timestamp(1)
	consensus.TargetWindow = consensus.BlockHeight(1000)
	network.BootstrapPeers = []network.Address{"localhost:9988"}
	consensus.RootTarget[0] = 255
	consensus.MaxAdjustmentUp = big.NewRat(1005, 1000)
	consensus.MaxAdjustmentDown = big.NewRat(995, 1000)

	// Pull together the configuration for the Core.
	state, _ := consensus.CreateGenesisState() // The missing piece is not of type error. TODO: That missing piece is deprecated.
	walletFilename := "test.wallet"
	w, err := wallet.New(state, walletFilename)
	if err != nil {
		t.Fatal(err)
	}
	hdb, err := hostdb.New()
	if err != nil {
		t.Fatal(err)
	}
	Host, err := host.New(state, w)
	if err != nil {
		return
	}
	Renter, err := renter.New(state, hdb, w)
	if err != nil {
		return
	}
	coreConfig := Config{
		HostDir:     "hostdir",
		WalletFile:  walletFilename,
		ServerAddr:  ":9988",
		Nobootstrap: true,

		State: state,

		Host:   Host,
		HostDB: hdb,
		Miner:  miner.New(),
		Renter: Renter,
		Wallet: w,
	}

	// Create the core.
	c, err = CreateCore(coreConfig)
	if err != nil {
		t.Fatal(err)
	}

	return
}

func TestEverything(t *testing.T) {
	c := establishTestingEnvironment(t)
	testEmptyBlock(t, c)
	testTransactionBlock(t, c)
	testSendToSelf(t, c)
	testWalletInfo(t, c)
	testHostAnnouncement(t, c)
	testUploadFile(t, c)
	sendManyTransactions(t, c)
	testMinerDeadlocking(t, c)
}
