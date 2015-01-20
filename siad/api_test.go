package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/NebulousLabs/Sia/consensus"
	"github.com/NebulousLabs/Sia/network"
	"github.com/NebulousLabs/Sia/sia"
	"github.com/NebulousLabs/Sia/sia/components"
)

type SuccessResponse struct {
	Success bool
}

// httpReq will request a byte stream from the provided url then log and return
// any errors
func httpReq(t *testing.T, url string) ([]byte, error) {
	resp, err := http.Get("http://127.0.0.1:9980" + url)
	if err != nil {
		t.Log("Could not make http request to " + url)
		return nil, err
	}
	defer resp.Body.Close()

	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Log("Could not read HTTP response from " + url + " : " + err.Error())
		return nil, err
	}
	if resp.StatusCode != 200 {
		t.Log("HTTP Response from " + url + " returned: " + strconv.Itoa(resp.StatusCode) + string(content))
		return nil, err
	}
	return content, nil
}

// reqJSON will send an http request to the provided url and fill the response
// struct
func reqJSON(t *testing.T, url string, response interface{}) {
	content, err := httpReq(t, url)
	if err != nil {
		t.Fatal(err.Error())
	}
	err = json.Unmarshal(content, &response)
	if err != nil {
		t.Fatal("Could not parse json response to " + url + ": " + err.Error())
	}
	return
}

func reqSuccess(t *testing.T, url string) SuccessResponse {
	var response SuccessResponse
	reqJSON(t, url, &response)
	return response
}

func reqWalletStatus(t *testing.T) components.WalletInfo {
	var r components.WalletInfo
	reqJSON(t, "/wallet/status", &r)
	return r
}

func reqHostConfig(t *testing.T) components.HostInfo {
	var r components.HostInfo
	reqJSON(t, "/host/config", &r)
	return r
}

func reqMinerStatus(t *testing.T) components.MinerInfo {
	var r components.MinerInfo
	reqJSON(t, "/miner/status", &r)
	return r
}

func reqWalletAddress(t *testing.T) struct{ Address string } {
	var r struct{ Address string }
	reqJSON(t, "/wallet/address", &r)
	return r
}

func reqGenericStatus(t *testing.T) sia.StateInfo {
	var r sia.StateInfo
	reqJSON(t, "/status", &r)
	return r
}

func reqFileStatus(t *testing.T) components.RentInfo {
	var r components.RentInfo
	reqJSON(t, "/file/status", &r)
	return r
}

func reqHostSetConfig(t *testing.T, hostInfo components.HostInfo) SuccessResponse {
	// return reqSuccess(t, "/host/setconfig")
	var params url.Values
	params.Add("totalstorage", fmt.Sprintf("%d", hostInfo.Announcement.TotalStorage))
	params.Add("maxfilesize", fmt.Sprintf("%d", hostInfo.Announcement.MaxFilesize))
	params.Add("mintolerance", fmt.Sprintf("%d", hostInfo.Announcement.MinTolerance))
	params.Add("maxduration", fmt.Sprintf("%d", hostInfo.Announcement.MaxDuration))
	params.Add("price", fmt.Sprintf("%d", hostInfo.Announcement.Price))
	params.Add("burn", fmt.Sprintf("%d", hostInfo.Announcement.Burn))

	urlWithParams := "http://127.0.0.1:9980/host/setconfig?" + params.Encode()

	resp, err := http.Get(urlWithParams)
	if err != nil {
		t.Fatal("Couldn't set host config: " + err.Error())
	}

	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatal("Could not read set host config response: " + err.Error())
	}

	var fResponse SuccessResponse

	err = json.Unmarshal(content, &fResponse)
	if err != nil {
		t.Fatal("Could not parse set host config response: " + err.Error())
	}

	return fResponse
}

func reqMinerStart(t *testing.T, threads int) SuccessResponse {
	return reqSuccess(t, "/miner/start?threads="+fmt.Sprintf("%d", threads))
}

func reqMinerStop(t *testing.T) SuccessResponse {
	return reqSuccess(t, "/miner/stop")
}

func reqWalletSend(t *testing.T, amount int, address string) SuccessResponse {
	return reqSuccess(t, "/wallet/send?dest="+address+"&amount="+fmt.Sprintf("%d", amount))
}

func reqAddPeer(t *testing.T, peerAddr string) SuccessResponse {
	return reqSuccess(t, "/peer/add?addr="+peerAddr)
}

func reqRemovePeer(t *testing.T, peerAddr string) SuccessResponse {
	return reqSuccess(t, "/peer/remove?addr="+peerAddr)
}

// This might kill the program...
func reqApplyUpdate(t *testing.T) SuccessResponse {
	return reqSuccess(t, "/update/apply")
}

func reqCheckUpdate(t *testing.T) (Available bool, Version string) {
	var response struct {
		Version   string
		Available bool
	}
	reqJSON(t, "/update/check", &response)
	return response.Available, response.Version
}

func reqUploadFile(t *testing.T, localpath string, nickname string, filename string, pieces int) {
	// Read in file
	file, err := os.Open(localpath)
	if err != nil {
		t.Fatal("Couldn't open file to upload: " + err.Error())
	}
	defer file.Close()

	// Write file to form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal("Error creating form file: " + err.Error())
	}
	_, err = io.Copy(part, file)

	// Populate other parameters
	writer.WriteField("filename", filename)
	writer.WriteField("nickname", nickname)
	writer.WriteField("pieces", strconv.Itoa(pieces))

	err = writer.Close()
	if err != nil {
		t.Fatal("Error closing writer: " + err.Error())
	}

	// Create the http request
	req, err := http.NewRequest("POST", "http://127.0.0.1:9980/file/upload", body)
	if err != nil {
		t.Fatal("Error creating POST request: " + err.Error())
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Actually make the request
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal("Error creating http client: " + err.Error())
	}

	// Read response body
	resContent, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatal("Error reading response content: " + err.Error())
	}

	// Check the response
	if res.StatusCode != http.StatusOK {
		t.Fatal("/file/upload returne " + strconv.Itoa(res.StatusCode) + ": " + string(resContent))
	}

	//TODO: Read response into JSON success object
	//TODO: return success object
}

func reqDownloadFile(t *testing.T, nickname string, filename string) SuccessResponse {
	return reqSuccess(t, "/file/download?nickname="+nickname+"&filename="+filename)
}

func reqStop(t *testing.T) {
	httpReq(t, "/stop")
}

func setupDaemon(t *testing.T) {

	// Settings to speed up operations
	consensus.BlockFrequency = consensus.Timestamp(1)
	consensus.TargetWindow = consensus.BlockHeight(1000)
	network.BootstrapPeers = []network.Address{"localhost:9988"}
	consensus.RootTarget[0] = 255
	consensus.MaxAdjustmentUp = big.NewRat(1005, 1000)
	consensus.MaxAdjustmentDown = big.NewRat(995, 1000)

	var config Config

	config.Siad.ConfigFilename = filepath.Join(siaDir, "config")
	config.Siacore.HostDirectory = filepath.Join(siaDir, "hostdir")
	config.Siad.StyleDirectory = filepath.Join(siaDir, "style")
	config.Siad.DownloadDirectory = "~/Downloads"
	config.Siad.WalletFile = filepath.Join(siaDir, "test.wallet")
	config.Siad.APIaddr = "localhost:9980"
	config.Siacore.RPCaddr = ":9988"
	config.Siacore.NoBootstrap = false
	err := config.expand()
	if err != nil {
		t.Fatal("Couldn't expand config: " + err.Error())
	}

	go func() {
		err = startDaemon(config)
		if err != nil {
			t.Fatal("Couldn't start daemon: " + err.Error())
		}
	}()

	// Give the daemon time to initialize
	time.Sleep(10 * time.Millisecond)

	// First call is just to see if daemon booted
	_, err = httpReq(t, "/wallet/status")
	if err != nil {
		t.Fatal("Daemon could not handle first request (after 10ms) " + err.Error())
	}
}

// Tests that the initial status of the daemon is sane - additionally tests that
// each component can return it's status
func TestBasicStatus(t *testing.T) {
	setupDaemon(t)

	// Test getting miner status
	mInfo := reqMinerStatus(t)
	if mInfo.State != "Off" {
		t.Fatal("Miner initial state was not off: (" + strconv.Itoa(mInfo.Threads) + ")")
	}
	if mInfo.RunningThreads != 0 {
		t.Fatal("Miner initial RunningThreads was not 0: (" + strconv.Itoa(mInfo.Threads) + ")")
	}
	if mInfo.Threads != 1 {
		t.Fatal("Miner initial Threads was not 1 (" + strconv.Itoa(mInfo.Threads) + ")")
	}

	// Test getting wallet status
	wInfo := reqWalletStatus(t)
	if wInfo.Balance > 0 {
		t.Fatal("Miner wallet started with initial balance greater than 0")
	}
	if wInfo.FullBalance > 0 {
		t.Fatal("Miner full balance started with initial balance greater than 0")
	}

	// Test getting general status
	gInfo := reqGenericStatus(t)
	if gInfo.Height > 0 {
		t.Fatal("Block height started greater than 0")
	}

	// Test getting host config
	hConfig := reqHostConfig(t)
	if hConfig.Announcement.TotalStorage > 0 {
		t.Fatal("Host started with more than 0 TotalStorage!")
	}

	// TODO: we need some way of guranteeing that the daemon will stop so that
	// other tests can be run
}

// TODO: this test can't be run because we have no way of shutting off the other
// daemon ATM
// Tests that the miner is able to mine i.e. gets some coins after a couple
// ms (this should be easy given the difficulty settings)
/*func TestBasicMining(t *testing.T) {
	setupDaemon(t)

	reqMinerStart(t, 2)

	time.Sleep(5 * time.Millisecond)

	w := reqWalletStatus(t)

	if w.Balance == 0 {
		t.Fatal("Miner wasn't able to mine any coins!")
	}

	// TODO: we need some way of guaranteeing that the daemon will stop so that
	// other tests can be run
	reqStop(t)
}*/
