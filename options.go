package btmeater

import "github.com/fako1024/gatt"

// WithDeviceID sets the Bluetooth device ID
func WithDeviceID(deviceID string) func(*Meater) {
	return func(f *Meater) {
		f.deviceID = deviceID
	}
}

// WithDeviceName sets the Bluetooth device name
func WithDeviceName(deviceName string) func(*Meater) {
	return func(f *Meater) {
		f.deviceName = deviceName
	}
}

// WithDevice sets the Bluetooth device
func WithDevice(btDevice gatt.Device) func(*Meater) {
	return func(f *Meater) {
		f.btDevice = btDevice
	}
}

// WithLogger sets a logger
func WithLogger(logger Logger) func(*Meater) {
	return func(f *Meater) {
		f.logger = logger
	}
}
