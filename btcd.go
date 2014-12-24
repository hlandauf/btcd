// Copyright (c) 2013-2014 Conformal Systems LLC.
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"runtime"

  "github.com/hlandauf/btcserver"
	"github.com/hlandauf/btcd/limits"
	"github.com/hlandau/degoutils/service"
  "github.com/hlandau/xlog"
)

var log, Log = xlog.New("BTCD")
var shutdownChannel = make(chan struct{})

// btcdMain is the real main function for btcd.  It is necessary to work around
// the fact that deferred functions do not run when os.Exit() is called.  The
// optional serverChan parameter is mainly used by the service code to be
// notified with the server once it is setup so it can gracefully stop it when
// requested from the service control manager.
func btcdMain(serverChan chan<- *btcserver.Server) error {
	// Load configuration and parse command line.  This function also
	// initializes logging and configures it accordingly.
	tcfg, _, err := loadConfig()
	if err != nil {
		return err
	}
  cfg := tcfg
	defer xlog.Flush()

	// Show version at startup.
	//log.Infof("Version %s", version())

	// Load the block database.
	db, err := cfg.LoadBlockDB()
	if err != nil {
		log.Errorf("%v", err)
		return err
	}
	defer db.Close()

	cfg.NodeConfig.DB = db

	// Create server and start it.
	server, err := btcserver.New(cfg)
	if err != nil {
		// TODO(oga) this logging could do with some beautifying.
		log.Errorf("Unable to start server on %v: %v",
			cfg.Listeners, err)
		return err
	}

	server.Start()
	if serverChan != nil {
		serverChan <- server
	}

	// Monitor for graceful server shutdown and signal the main goroutine
	// when done. This is done in a separate goroutine rather than waiting
	// directly so the main goroutine can be signaled for shutdown by either
	// a graceful shutdown or from the main interrupt handler. This is
	// necessary since the main goroutine must be kept running long enough
	// for the interrupt handler goroutine to finish.
	go func() {
		server.WaitForShutdown()
		log.Infof("Server shutdown complete")
		shutdownChannel <- struct{}{}
	}()

	// Wait for shutdown signal from either a graceful server stop or from
	// the interrupt handler.
	<-shutdownChannel
	log.Infof("Gracefully shutting down the database...")
	db.RollbackClose()
	log.Infof("Shutdown complete")
	return nil
}

func main() {
	// Use all processor cores.
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Up some limits.
	if err := limits.SetLimits(); err != nil {
		os.Exit(1)
	}

	service.Main(&service.Info{
		Name: "btcd",
		Description: "Go-language full node Bitcoin daemon",
		RunFunc: func(smgr service.Manager) error {

			// btcdMain sends *Server on schan once it has finished
			// starting.
			schan := make(chan *btcserver.Server)
			doneChan := make(chan error)
			go func() {
				doneChan <- btcdMain(schan)
			}()

			var s *btcserver.Server

			select {
			case err := <-doneChan:
				// premature exit
				return err
			case s = <-schan:
			}

			// server started, drop privileges and notify
			err := smgr.DropPrivileges()
			if err != nil {
				return err
			}

			smgr.SetStarted()

			// wait for stop or spontaneous exit
			select {
				case <-smgr.StopChan():
					s.Stop()
					return <-doneChan
				case err := <-doneChan:
					// spontaneous exit
					return err
			}
		},
	})
}
