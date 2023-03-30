package btmeater

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fako1024/gatt"
)

const (
	defaultDeviceName = "MEATER"

	probeInfoService                   = "180a"
	probeManufacturerCharacteristic    = "2a29"
	probeModelNumberCharacteristic     = "2a24"
	probeFirmwareVersionCharacteristic = "2a26"
	probeSoftwareVersionCharacteristic = "2a28"

	dataService           = "a75cc7fcc956488fac2a2dbc08b63a04"
	tempCharacteristic    = "7edda774045e4bbf909b45d1991a2876"
	batteryCharacteristic = "2adb487768d84884bd3cd83853bf27b8"
)

// Meater denotes a Meater bluetooth temperature probe
type Meater struct {
	connectionStatus ConnectionStatus
	batteryLevel     int

	deviceID   string
	deviceName string

	probeInfo ProbeInfo

	stateChangeHandler func(status ConnectionStatus)
	stateChangeChan    chan ConnectionStatus

	doneChan chan struct{}

	btDevice                gatt.Device
	btPeripheral            gatt.Peripheral
	btTempCharacteristic    *gatt.Characteristic
	btBatteryCharacteristic *gatt.Characteristic

	logger Logger
}

// New instantiates a new Meater struct, executing functional options, if any
func New(options ...func(*Meater)) (*Meater, error) {

	// Initialize a new instance of a Meater
	f := &Meater{
		deviceName: defaultDeviceName,
		doneChan:   make(chan struct{}),
		logger:     &NullLogger{},
	}

	// Execute functional options (if any), see options.go for implementation
	for _, option := range options {
		option(f)
	}

	// Initialize a new GATT device (if not provided as option)
	if f.btDevice == nil {
		btDevice, err := gatt.NewDevice(defaultBTClientOptions...)
		if err != nil {
			return nil, err
		}
		f.btDevice = btDevice
	}

	return f, f.subscribe()
}

// ConnectionStatus returns the current status of the bluetooth device
func (f *Meater) ConnectionStatus() ConnectionStatus {
	return f.connectionStatus
}

// ProbeInfo returns the set of immutable information about the probe
func (f *Meater) ProbeInfo() ProbeInfo {
	return f.probeInfo
}

// BatteryLevel returns the current battery level
func (f *Meater) BatteryLevel() float64 {
	return float64(f.batteryLevel * 10)
}

// BatteryLevelRaw returns the current battery level in its raw form
func (f *Meater) BatteryLevelRaw() int {
	return f.batteryLevel
}

// SetStateChangeHandler defines a handler function that is called upon state change
func (f *Meater) SetStateChangeHandler(fn func(status ConnectionStatus)) {
	f.stateChangeHandler = fn
}

// SetStateChangeChannel defines a handler function that is called upon state change
func (f *Meater) SetStateChangeChannel(ch chan ConnectionStatus) {
	f.stateChangeChan = ch
}

// ReadData extracts the current data point from the probe
func (f *Meater) ReadData() (*DataPoint, error) {

	if status := f.ConnectionStatus(); status.State != StateConnected {
		return nil, fmt.Errorf("cannot read data unless device is connected (current status: %#v)", status)
	}

	rawData, err := f.btPeripheral.ReadCharacteristic(f.btTempCharacteristic)
	if err != nil {
		return nil, err
	}

	return parseTemperatureData(rawData)
}

// Close terminates the connection to the device
func (f *Meater) Close() error {
	close(f.doneChan)

	f.btDevice.StopScanning()
	return f.btDevice.RemoveAllServices()
}

////////////////////////////////////////////////////////////////////////////////

func (f *Meater) subscribe() error {

	// Register handlers
	f.btDevice.Handle(
		gatt.AddPeripheralDiscovered(f.genOnPeriphDiscovered()),
		gatt.AddPeripheralConnected(f.onPeriphConnected),
		gatt.AddPeripheralDisconnected(f.onPeriphDisconnected),
	)

	// Initialize the device
	return f.btDevice.Init(f.onStateChanged)
}

func (f *Meater) setStatus(state State, err error) {
	f.connectionStatus = ConnectionStatus{
		State: state,
		Error: err,
	}

	// Call handler function, if any
	if f.stateChangeHandler != nil {
		f.stateChangeHandler(f.connectionStatus)
	}

	// Put state change on channel, if any
	if f.stateChangeChan != nil {
		select {
		case f.stateChangeChan <- f.connectionStatus:
		default:
		}
	}
}

////////////////////////////////////////////////////////////////////////////////

func (f *Meater) onStateChanged(d gatt.Device, s gatt.State) {
	switch s {
	case gatt.StatePoweredOn:
		f.setStatus(StateScanning, nil)
		if err := d.Scan([]gatt.UUID{}, false); err != nil {
			f.logger.Warnf("failed to enable initial scanning: %s", err)
		}
		return
	case gatt.StatePoweredOff:
		f.setStatus(StateDisconnected, nil)
		return
	default:
		if err := d.StopScanning(); err != nil {
			f.logger.Warnf("failed to stop initial scanning: %s", err)
		}
	}
}

func (f *Meater) genOnPeriphDiscovered() func(p gatt.Peripheral, arg2 *gatt.Advertisement, arg3 int) {
	return func(p gatt.Peripheral, arg2 *gatt.Advertisement, arg3 int) {

		f.logger.Debugf("discovered device `%s/%s`", p.Name(), p.ID())

		// Check if name and / or device ID have been overridden
		if !f.thisDevice(p) {
			return
		}

		f.logger.Debugf("connecting device `%s/%s`", p.Name(), p.ID())

		// Stop scanning once we've got the peripheral we're looking for.
		if err := p.Device().StopScanning(); err != nil {
			f.logger.Warnf("failed to stop initial scanning: %s", err)
		}
		if err := p.Device().Connect(p); err != nil {
			f.logger.Errorf("Failed to connect device `%s/%s`: %s", p.Name(), p.ID(), err)
		}

		f.logger.Debugf("connected device `%s/%s`", p.Name(), p.ID())
	}
}

func (f *Meater) onPeriphConnected(p gatt.Peripheral, connErr error) {

	if !f.thisDevice(p) {
		return
	}

	f.logger.Debugf("connected peripheral `%s/%s`", p.Name(), p.ID())

	f.setStatus(StateConnected, nil)
	defer func() {
		p.Device().CancelConnection(p)
		f.setStatus(StateDisconnected, connErr)
	}()

	// Discover services
	ss, err := p.DiscoverServices([]gatt.UUID{
		gatt.MustParseUUID(probeInfoService),
		gatt.MustParseUUID(dataService),
	})
	if err != nil {
		connErr = fmt.Errorf("failed to discover services: %w", err)
		return
	}

	for _, s := range ss {

		if s.UUID().String() == probeInfoService {

			// Discover characteristics
			cs, err := p.DiscoverCharacteristics([]gatt.UUID{
				gatt.MustParseUUID(probeManufacturerCharacteristic),
				gatt.MustParseUUID(probeModelNumberCharacteristic),
				gatt.MustParseUUID(probeSoftwareVersionCharacteristic),
				gatt.MustParseUUID(probeFirmwareVersionCharacteristic),
			}, s)
			if err != nil {
				connErr = fmt.Errorf("failed to discover device info characteristics: %w", err)
				return
			}
			for _, c := range cs {

				switch c.UUID().String() {
				case probeManufacturerCharacteristic:
					{
						rawData, err := readCharacteristic(p, c, -1)
						if err != nil {
							connErr = fmt.Errorf("failed to read probe manufacturer characteristic: %w", err)
							return
						}
						f.probeInfo.Manufacturer = string(rawData)
					}
				case probeModelNumberCharacteristic:
					{
						rawData, err := readCharacteristic(p, c, -1)
						if err != nil {
							connErr = fmt.Errorf("failed to read probe model characteristic: %w", err)
							return
						}
						f.probeInfo.Model = string(rawData)
					}
				case probeSoftwareVersionCharacteristic:
					{
						rawData, err := readCharacteristic(p, c, -1)
						if err != nil {
							connErr = fmt.Errorf("failed to read probe software version characteristic: %w", err)
							return
						}
						f.probeInfo.SoftwareVersion = string(rawData)
					}
				case probeFirmwareVersionCharacteristic:
					{
						rawData, err := readCharacteristic(p, c, -1)
						if err != nil {
							connErr = fmt.Errorf("failed to read probe firmware version characteristic: %w", err)
							return
						}

						firmwareID := strings.Split(string(rawData), "_")
						if len(firmwareID) != 2 {
							connErr = fmt.Errorf("failed to parse probe firmware / ID string (`%s`)", string(rawData))
							return
						}

						f.probeInfo.FirmwareVersion = firmwareID[0]
						id, err := strconv.ParseInt(firmwareID[1], 10, 64)
						if err != nil {
							connErr = fmt.Errorf("failed to parse numeric probe ID (`%s`)", firmwareID[1])
							return
						}
						f.probeInfo.ID = int(id)
					}
				}
			}
		}

		if s.UUID().String() == dataService {
			f.btPeripheral = p

			// Discover characteristics
			cs, err := p.DiscoverCharacteristics([]gatt.UUID{
				gatt.MustParseUUID(batteryCharacteristic),
				gatt.MustParseUUID(tempCharacteristic),
			}, s)
			if err != nil {
				connErr = fmt.Errorf("failed to discover device data characteristics: %w", err)
				return
			}
			for _, c := range cs {
				if c.UUID().String() == batteryCharacteristic {

					f.btBatteryCharacteristic = c

					// Discover descriptors
					_, err := p.DiscoverDescriptors(nil, c)
					if err != nil {
						connErr = fmt.Errorf("failed to discover battery data descriptors: %w", err)
						return
					}

					rawBatteryData, err := p.ReadCharacteristic(c)
					if err != nil || len(rawBatteryData) != 2 {
						connErr = fmt.Errorf("failed to read battery data: %w (data len: %d)", err, len(rawBatteryData))
						return
					}
					f.batteryLevel = parseBatteryLevel(rawBatteryData)

					if err := p.SetNotifyValue(c, f.receiveBatteryData); err != nil {
						connErr = fmt.Errorf("failed to subscribe to battery data characteristic: %w", err)
						return
					}
				}

				if c.UUID().String() == tempCharacteristic {
					f.btTempCharacteristic = c

					// Discover descriptors
					// _, err := p.DiscoverDescriptors(nil, c)
					// if err != nil {
					// 	connErr = fmt.Errorf("failed to discover temperature data descriptors: %w", err)
					// 	return
					// }
				}
			}
		}
	}

	f.logger.Debugf("waiting to release peripheral `%s/%s`", p.Name(), p.ID())
	<-f.doneChan
	f.logger.Debugf("released peripheral `%s/%s`", p.Name(), p.ID())
}

func (f *Meater) onPeriphDisconnected(p gatt.Peripheral, err error) {

	if !f.thisDevice(p) {
		return
	}

	f.disconnect()
	f.logger.Debugf("disconnected peripheral `%s/%s`", p.Name(), p.ID())

	time.Sleep(100 * time.Millisecond)
	f.setStatus(StateScanning, nil)
	if err := f.btDevice.Scan([]gatt.UUID{gatt.MustParseUUID(dataService)}, false); err != nil {
		f.logger.Warnf("failed to re-enable scanning after disconnect: %s", err)
	}
}

func (f *Meater) thisDevice(p gatt.Peripheral) bool {

	// Check if name and / or device ID have been overridden
	if f.deviceID != "" && strings.EqualFold(p.ID(), f.deviceID) {
		return true
	}

	return strings.EqualFold(p.Name(), f.deviceName)
}

func (f *Meater) disconnect() {
	select {
	case f.doneChan <- struct{}{}:
	default:
	}
}

func min(a, b int) int {
	if a <= b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a >= b {
		return a
	}
	return b
}

func (f *Meater) receiveBatteryData(c *gatt.Characteristic, req []byte, err error) {
	if err != nil || len(req) != 2 {
		return
	}

	f.batteryLevel = parseBatteryLevel(req)
}

////////////////////////////////////////////////////////////////////////////////

func readCharacteristic(p gatt.Peripheral, c *gatt.Characteristic, expectedLen int) ([]byte, error) {
	// Discover descriptors
	_, err := p.DiscoverDescriptors(nil, c)
	if err != nil {
		return nil, fmt.Errorf("failed to discover battery data descriptors: %w", err)
	}

	rawData, err := p.ReadLongCharacteristic(c)
	if err != nil || (expectedLen >= 0 && len(rawData) != expectedLen) {
		return nil, fmt.Errorf("failed to read battery data: %w (data len: %d)", err, len(rawData))
	}

	return rawData, nil
}

func parseTemperatureData(data []byte) (*DataPoint, error) {
	if len(data) != 8 {
		return nil, fmt.Errorf("invalid length of data (want 8, have %d)", len(data))
	}

	tip := byteToInt(data[0], data[1])
	ra := byteToInt(data[2], data[3])
	oa := byteToInt(data[4], data[5])

	ambient := tip + (max(0, (((ra-min(48, oa))*16)*589)/1487))

	return &DataPoint{
		TimeStamp:          time.Now(),
		TemperatureTip:     toCelsius(tip),
		TemperatureAmbient: toCelsius(ambient),
	}, nil
}

func parseBatteryLevel(data []byte) int {
	return byteToInt(data[0], data[1])
}

func byteToInt(b0, b1 byte) int {
	return int(b1)*256 + int(b0)
}

func toCelsius(value int) float64 {
	return (float64(value) + 8.0) / 16.0
}
