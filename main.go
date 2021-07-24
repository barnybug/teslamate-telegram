package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
)

// 61% 334.87km 73.5 kWh usuable
const RatedKMPerKwh = 7.47
const KMPerMile = 1.61

type CarState struct {
	at                   time.Time
	geofence             string
	chargerPower         int
	chargerVoltage       int
	timeToFullCharge     float32
	chargerActualCurrent int
	chargeEnergyAdded    float32
	estBatteryRangeKm    float32
	ratedBatteryRangeKm  float32
	idealBatteryRangeKm  float32
	batteryLevel         int
	shiftState           string
	odometer             float32
	outsideTemp          float32
	insideTemp           float32
	pluggedIn            bool
	latitude             float32
	longitude            float32
}

func truncate(s string, limit int) string {
	// try to cut at a comma
	if len(s) < limit {
		return s
	}
	l := strings.LastIndex(s[:limit], ",")
	if l != -1 {
		limit = l
	}
	return s[:limit]
}

func (s CarState) placeName() string {
	if s.geofence != "" {
		return s.geofence
	}
	result, err := nominatimLookup(s.latitude, s.longitude)
	if err == nil {
		name := result.Name
		if name == "" {
			name = result.DisplayName
		}
		if name != "" {
			name = truncate(name, 20)
			return name
		}
	}
	return "?"
}

type Car struct {
	displayName string
	state       string
	carState    CarState

	charging    bool
	chargeStart CarState
	chargePeak  CarState

	driving    bool
	driveStart CarState

	update *time.Timer
}

func (car *Car) Update(key string, value string) {
	car.carState.at = time.Now()
	switch key {
	case "display_name":
		car.displayName = value
	case "state":
		car.state = value
	case "shift_state":
		car.carState.shiftState = value
	case "geofence":
		car.carState.geofence = value
	case "charger_power":
		if ivalue, err := strconv.Atoi(value); err == nil {
			car.carState.chargerPower = ivalue
		}
	case "charger_voltage":
		if ivalue, err := strconv.Atoi(value); err == nil {
			car.carState.chargerVoltage = ivalue
		}
	case "time_to_full_charge":
		if fvalue, err := strconv.ParseFloat(value, 32); err == nil {
			car.carState.timeToFullCharge = float32(fvalue)
		}
	case "charger_actual_current":
		if ivalue, err := strconv.Atoi(value); err == nil {
			car.carState.chargerActualCurrent = ivalue
		}
	case "charge_energy_added":
		if fvalue, err := strconv.ParseFloat(value, 32); err == nil {
			car.carState.chargeEnergyAdded = float32(fvalue)
		}
	case "est_battery_range_km":
		if fvalue, err := strconv.ParseFloat(value, 32); err == nil {
			car.carState.estBatteryRangeKm = float32(fvalue)
		}
	case "ideal_battery_range_km":
		if fvalue, err := strconv.ParseFloat(value, 32); err == nil {
			car.carState.idealBatteryRangeKm = float32(fvalue)
		}
	case "rated_battery_range_km":
		if fvalue, err := strconv.ParseFloat(value, 32); err == nil {
			car.carState.ratedBatteryRangeKm = float32(fvalue)
		}
	case "battery_level":
		if ivalue, err := strconv.Atoi(value); err == nil {
			car.carState.batteryLevel = ivalue
		}
	case "odometer":
		if fvalue, err := strconv.ParseFloat(value, 32); err == nil {
			car.carState.odometer = float32(fvalue)
		}
	case "outside_temp":
		if fvalue, err := strconv.ParseFloat(value, 32); err == nil {
			car.carState.outsideTemp = float32(fvalue)
		}
	case "inside_temp":
		if fvalue, err := strconv.ParseFloat(value, 32); err == nil {
			car.carState.insideTemp = float32(fvalue)
		}
	case "plugged_in":
		car.carState.pluggedIn = (value == "true")
	case "latitude":
		if fvalue, err := strconv.ParseFloat(value, 32); err == nil {
			car.carState.latitude = float32(fvalue)
		}
	case "longitude":
		if fvalue, err := strconv.ParseFloat(value, 32); err == nil {
			car.carState.longitude = float32(fvalue)
		}
	}
}

func driveShiftState(s string) bool {
	return s == "D" || s == "R"
}

func efficiency(start, end CarState) float32 {
	kwh := (start.ratedBatteryRangeKm - end.ratedBatteryRangeKm) / RatedKMPerKwh
	return kwh * 1000 / (end.odometer - start.odometer) * KMPerMile // Wh/mi
}

func clientOptions() *mqtt.ClientOptions {
	hostname, _ := os.Hostname()
	clientID := fmt.Sprintf("teslamate-telegram-%s", hostname)
	opts := mqtt.NewClientOptions()
	opts.AddBroker("tcp://mqtt:1883")
	opts.SetClientID(clientID)  // set unique client id
	opts.SetAutoReconnect(true) // auto reconnect (default)
	opts.SetCleanSession(false) // server will queue messages whilst client is offline
	return opts
}

func main() {
	// discover cars
	carUpdates := make(chan *Car, 1)
	var defaultCar int
	cars := map[int]*Car{}
	carHandler := func(client mqtt.Client, msg mqtt.Message) {
		var carId int
		var key string
		_, err := fmt.Sscanf(msg.Topic(), "teslamate/cars/%d/%s", &carId, &key)
		if err != nil {
			log.Println("Failed to parse topic:", msg.Topic())
			return
		}
		var car *Car
		var exists bool
		if car, exists = cars[carId]; !exists {
			log.Printf("New car discovered %d: %s\n", carId, msg.Payload())
			car = &Car{
				update: time.NewTimer(2 * time.Second),
			}
			cars[carId] = car
			go func() {
				// relay update events to common channel
				for range cars[carId].update.C {
					carUpdates <- car
				}
			}()
			defaultCar = carId
		}
		car.Update(key, string(msg.Payload()))
		car.update.Reset(time.Second)
	}

	opts := clientOptions()
	opts.SetOnConnectHandler(func(client mqtt.Client) {
		if token := client.Subscribe("teslamate/cars/#", 0, carHandler); token.Wait() && token.Error() != nil {
			panic(token.Error())
		}
	})
	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}
	log.Println("Connected to mqtt")

	token := os.Getenv("TELEGRAM_TOKEN")
	chatId, _ := strconv.ParseInt(os.Getenv("TELEGRAM_CHAT_ID"), 10, 64)
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatalf("Error connecting to telegram: %s", err)
	}

	log.Printf("Telegram authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	botUpdates, err := bot.GetUpdatesChan(u)

	for {
		select {
		case update := <-botUpdates:
			if update.Message == nil {
				break
			}
			log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)

			switch update.Message.Command() {
			case "status":
				car := cars[defaultCar]
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, statusMessage(car))
				bot.Send(msg)
			default:
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, fmt.Sprintf("Hello. Set TELEGRAM_CHAT_ID=%d", update.Message.Chat.ID))
				msg.ReplyToMessageID = update.Message.MessageID
				bot.Send(msg)
			}
		case car := <-carUpdates:
			log.Printf("State update: %+v", car.carState)
			if car.charging && car.carState.chargerPower == 0 {
				log.Printf("Finished charging: %+v", car.carState)
				car.charging = false
				text := finishChargingMessage(car.chargeStart, car.carState, car.chargePeak)
				if text == "" {
					break
				}
				msg := tgbotapi.NewMessage(chatId, text)
				msg.ParseMode = "HTML"
				bot.Send(msg)
			} else if car.charging && car.carState.chargerPower > car.chargePeak.chargerPower {
				car.chargePeak = car.carState
				log.Printf("New charging peak: %+v", car.carState)
			} else if !car.charging && car.carState.chargerPower > 0 {
				log.Printf("Started charging: %+v", car.carState)
				car.charging = true
				car.chargeStart = car.carState
				car.chargePeak = car.carState
			}
			if driveShiftState(car.carState.shiftState) && !car.driving {
				// started driving
				log.Printf("Started driving: %+v", car.carState)
				car.driving = true
				car.driveStart = car.carState
			} else if !driveShiftState(car.carState.shiftState) && car.driving {
				// finished driving
				log.Printf("Finished driving: %+v", car.carState)
				car.driving = false
				text := finishDriveMessage(car.driveStart, car.carState)
				if text == "" {
					break
				}
				msg := tgbotapi.NewMessage(chatId, text)
				msg.ParseMode = "HTML"
				bot.Send(msg)
			}
		}
	}
}

func formatDuration(d time.Duration) string {
	u := uint64(d.Round(time.Minute)) / uint64(time.Minute)
	if u < 60 {
		return fmt.Sprintf("%dm", u)
	} else {
		return fmt.Sprintf("%dh%dm", u/60, u%60)
	}
}

func finishChargingMessage(start, end, peak CarState) string {
	battery := end.batteryLevel - start.batteryLevel
	if battery == 0 {
		return ""
	}
	duration := end.at.Sub(start.at)
	averagePower := float64(end.chargeEnergyAdded-start.chargeEnergyAdded) / duration.Hours()
	milesAdded := (end.ratedBatteryRangeKm - start.ratedBatteryRangeKm) / KMPerMile
	text := fmt.Sprintf("🔌 Charging finished at %s.\n🕗 %s→%s (%s)\n🔋 %d→%d%% (+ %d%%)\n🚗 %0.f→%.0f miles (+ %.1f miles).\n⚡ + %.1fkWh\nAverage Power: %.2fkW (Peak %dkW at %d%%)",
		start.placeName(),
		start.at.Format("15:04"), end.at.Format("15:04"), formatDuration(duration),
		start.batteryLevel, end.batteryLevel, battery,
		start.ratedBatteryRangeKm/KMPerMile, end.ratedBatteryRangeKm/KMPerMile, milesAdded,
		end.chargeEnergyAdded, averagePower, peak.chargerPower, peak.batteryLevel)
	return text
}

func finishDriveMessage(start, end CarState) string {
	distance := (end.odometer - start.odometer) / KMPerMile
	if distance < 0.1 {
		return ""
	}
	battery := end.batteryLevel - start.batteryLevel
	eff := efficiency(start, end)
	duration := end.at.Sub(start.at)
	miles := (start.ratedBatteryRangeKm - end.ratedBatteryRangeKm) / KMPerMile
	text := fmt.Sprintf("🚗 %s->%s <code>%.1f</code> miles 🌡 %.1f°C\n🕗 %s→%s (%s)\n🔋 %d→%d%% (%d%%)\n🚘 %0.f→%.0f miles (%.1f miles @ %.0fWh/mi)",
		start.placeName(), end.placeName(), distance,
		start.outsideTemp,
		start.at.Format("15:04"), end.at.Format("15:04"), formatDuration(duration),
		start.batteryLevel, end.batteryLevel, battery,
		start.ratedBatteryRangeKm/KMPerMile, end.ratedBatteryRangeKm/KMPerMile, miles,
		eff)
	return text
}

func statusMessage(car *Car) string {
	text := fmt.Sprintf("🔋%d%%", car.carState.batteryLevel)
	return text
}

type LookupResult struct {
	DisplayName string `json:"display_name"`
	Name        string `json:"name"`
}

func nominatimLookup(latitude, longitude float32) (*LookupResult, error) {
	query := url.Values{}
	query.Add("lat", fmt.Sprint(latitude))
	query.Add("lon", fmt.Sprint(longitude))
	query.Add("format", "jsonv2")
	query.Add("addressdetails", "0")
	uri := "https://nominatim.openstreetmap.org/reverse?" + query.Encode()
	resp, err := http.Get(uri)
	if err != nil {
		return nil, err
	}
	var result LookupResult
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
