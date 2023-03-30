package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fako1024/btmeater"
)

type config struct {
	name string
	addr string
}

func main() {

	logger := btmeater.NewDefaultLogger(false)

	// Parse command line options
	var (
		cfg config
		m   *btmeater.Meater
		err error
	)

	flag.StringVar(&cfg.name, "name", "MEATER", "name of remote peripheral")
	flag.StringVar(&cfg.addr, "addr", "", "address of remote peripheral (MAC on Linux, UUID on OS X)")
	flag.Parse()

	m, err = btmeater.New()
	if err != nil {
		logger.Fatalf("failed to initialize Meater device: %s", err)
	}

	stateChan := make(chan btmeater.ConnectionStatus)
	m.SetStateChangeChannel(stateChan)

	go func() {
		for st := range stateChan {
			logger.Infof("state change: %v", st)
			if st.State == btmeater.StateConnected {
				logger.Infof("got probe information: %#v", m.ProbeInfo())
				logger.Infof("got battery level: %f %%", m.BatteryLevel())
			}
		}
	}()

	sigChan := make(chan os.Signal, 32)
	signal.Notify(sigChan, syscall.SIGTERM)
	signal.Notify(sigChan, os.Interrupt)
	go func() {
		<-sigChan
		logger.Infof("got signal, terminating connection to device")
		if err := m.Close(); err != nil {
			logger.Errorf("failed to close device: %s", err)
		}
		os.Exit(0)
	}()

	for {
		time.Sleep(1 * time.Second)

		data, err := m.ReadData()
		if err != nil {
			logger.Errorf("error reading data: %s", err)
			continue
		}

		logger.Infof("got data: %s", data)
	}
}
