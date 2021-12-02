package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog/log"
)

var (
	endpoint   = flag.String("endpoint", "unix:///csi/csi.sock", "CSI endpoint")
	controller = flag.Bool("controller", false, "serve Controller Service RPC")
	nodeId     = flag.String("node", "", "node id (serve Node Service RPC)")
	stateFile  = flag.String("state", "/var/lib/universal-csi-driver/state.json", "state file (used by Node Service only)")
	mountPath  = flag.String("mount_path", "/data", "path for data inside driver's container to publish")
)

func main() {
	flag.Parse()

	driver := newDriver()
	if *controller {
		driver.initControllerServer()
	}
	if *nodeId != "" {
		err := driver.initNodeServer(*nodeId, *stateFile, *mountPath)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to init node server")
		}
	}

	log.Info().Msg("universal-csi-driver started")
	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigC)
	go func() {
		sig := <-sigC
		log.Info().Str("signal", sig.String()).Msg("signal received, terminating")
		signal.Stop(sigC)
		driver.stop()
	}()

	driver.run(*endpoint)
	log.Info().Msg("universal-csi-driver stopped")
}
