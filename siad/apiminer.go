package main

import (
	"fmt"
	"net/http"
)

// Takes a number of threads and then begins mining on that many threads.
func (d *daemon) minerStartHandler(w http.ResponseWriter, req *http.Request) {
	// Scan for the number of threads.
	var threads int
	_, err := fmt.Sscan(req.FormValue("threads"), &threads)
	if err != nil {
		http.Error(w, "Malformed number of threads", 400)
		return
	}

	d.core.UpdateMiner(threads)
	d.core.StartMining()

	writeSuccess(w)
}

// Returns json of the miners status.
func (d *daemon) minerStatusHandler(w http.ResponseWriter, req *http.Request) {
	mInfo, err := d.core.MinerInfo()
	if err != nil {
		http.Error(w, "Failed to encode status object", 500)
		return
	}
	writeJSON(w, mInfo)
}

// Calls StopMining() on the core.
func (d *daemon) minerStopHandler(w http.ResponseWriter, req *http.Request) {
	d.core.StopMining()

	writeSuccess(w)
}
