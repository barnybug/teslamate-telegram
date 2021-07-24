package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFinishChargingMessageHome(t *testing.T) {
	startAt := time.Date(2021, 4, 9, 6, 39, 0, 0, time.UTC)
	endAt := startAt.Add(90 * time.Minute)
	start := CarState{at: startAt, chargerPower: 7, chargeEnergyAdded: 0.0, batteryLevel: 50}
	end := CarState{at: endAt, chargerPower: 0, chargeEnergyAdded: 3.8, batteryLevel: 55}
	peak := CarState{chargerPower: 8, chargeEnergyAdded: 1, batteryLevel: 52}
	message := finishChargingMessage(start, end, peak)
	assert.Equal(t, message, "🔌 Charging finished at Soul Buoy.\n🕗 06:39→08:09 (1h30m)\n🔋 50→55% (+ 5%)\n🚗 0→0 miles (+ 0.0 miles).\n⚡ + 3.8kWh\nAverage Power: 2.53kW (Peak 8kW at 52%)")
}

func TestFinishChargingMessageZero(t *testing.T) {
	start := CarState{}
	end := CarState{}
	peak := CarState{}
	message := finishChargingMessage(start, end, peak)
	assert.Equal(t, message, "")
}

func TestFinishDriveMessage(t *testing.T) {
	startAt := time.Date(2021, 4, 9, 6, 39, 0, 0, time.UTC)
	endAt := startAt.Add(8 * time.Minute)
	start := CarState{at: startAt, chargerPower: 7, chargeEnergyAdded: 0.0, batteryLevel: 50, odometer: 976, outsideTemp: 7.5, ratedBatteryRangeKm: 400, geofence: "Home"}
	end := CarState{at: endAt, chargerPower: 0, chargeEnergyAdded: 3.8, batteryLevel: 48, odometer: 986, outsideTemp: 8.0, ratedBatteryRangeKm: 390, geofence: "", latitude: 52.3, longitude: 0.1}
	message := finishDriveMessage(start, end)
	assert.Equal(t, message, "🚗 Home->Cow Lane <code>6.2</code> miles 🌡 7.5°C\n🕗 06:39→06:47 (8m)\n🔋 50→48% (-2%)\n🚘 248→242 miles (6.2 miles @ 216Wh/mi)")
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "A", truncate("A", 20))
	assert.Equal(t, "3, Hurrell Road", truncate("3, Hurrell Road, Cambridge, Cambridgeshire, East of England, England, CB4 3RQ, United Kingdom", 20))
	assert.Equal(t, "A very long test wit", truncate("A very long test without a comma", 20))
}

func TestPlaceNameLookup(t *testing.T) {
	state := CarState{latitude: 52.223, longitude: 0.116}
	assert.Equal(t, "19, Acton Way", state.placeName())
}

func TestPlaceNameGeofence(t *testing.T) {
	state := CarState{latitude: 52.223, longitude: 0.116, geofence: "Home"}
	assert.Equal(t, "Home", state.placeName())
}
