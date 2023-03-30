//go:generate stringer -type=State -trimprefix=State
package btmeater

import (
	"fmt"
	"time"
)

// State denotes a connection state
type State int

const (

	// StateScanning is active while scanning for a bluetooth device
	StateScanning State = iota

	// StateConnected is active while being connected to the meater
	StateConnected

	// StateDisconnected is active after being disconnected from the meater
	StateDisconnected
)

// ConnectionStatus denotes the current status of the bluetooth device
type ConnectionStatus struct {
	Error error
	State
}

// ProbeInfo denotes immutable information about the probe
type ProbeInfo struct {
	ID int

	Manufacturer    string
	Model           string
	SoftwareVersion string
	FirmwareVersion string
}

// DataPoint denotes a temperature measurement at a certain point in time
type DataPoint struct {
	TimeStamp          time.Time
	TemperatureTip     float64
	TemperatureAmbient float64
}

// String fulfils the Stringer interface
func (d *DataPoint) String() string {
	return fmt.Sprintf("Tip: %.1f°C, Ambient: %.1f°C", d.TemperatureTip, d.TemperatureAmbient)
}

// DataPoints denotes a set of data points (usually part of a cooking process)
type DataPoints []DataPoint
