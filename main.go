package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

type config struct {
	botID     string
	sensorURI string
}

var globalConfig config

type message struct {
	Text string
	Chat *chat
}

type chat struct {
	ID int
}

type update struct {
	UpdateID int `json:"update_id"`
	Message  message
}

type sensor struct {
	Pres  int
	Ptemp float32
	Temp  int
	Hum   int
	CO2   int
}

type apiResponse struct {
	Ok     bool
	Result []update
}

type workerPool struct {
	workers map[int]worker
}

func (pool *workerPool) sendMsg(chatID int, msg string) {
	if w, ok := pool.workers[chatID]; ok {
		w.messages <- msg
	}
}

func (pool *workerPool) start(chatID, limit int) {
	pool.stop(chatID)
	messageChan := make(chan string)
	if pool.workers == nil {
		pool.workers = make(map[int]worker)
	}
	newWorker := worker{chatID: chatID, messages: messageChan, limit: limit}
	pool.workers[chatID] = newWorker
	delayDefault := 60
	dalayAfterAlert := 300
	go func() {
		delay := 0
		for {
			select {
			case msg := <-newWorker.messages:
				switch msg {
				case "stop":
					return
				case "/sleep 15 min":
					delay = 900
				case "/sleep 30 min":
					delay = 1800
				case "/sleep 1 hour":
					delay = 3600
				case "/sleep 2 hour":
					delay = 7200
				case "/sleep 5 hour":
					delay = 18000
				}
			case <-time.After(time.Duration(delay) * time.Second):
				delay = delayDefault
				sensorParams := getCO2()
				if sensorParams.CO2 > newWorker.limit {
					sendMessage(newWorker.chatID, "Achtung! CO2 is "+strconv.Itoa(sensorParams.CO2)+"!")
					delay = dalayAfterAlert
				}
			}
		}
	}()
}

type worker struct {
	messages chan string
	limit    int
	chatID   int
}

func (pool *workerPool) stop(chatID int) {
	pool.sendMsg(chatID, "stop")
	delete(pool.workers, chatID)
}

func main() {
	botIDPtr := flag.String("bot", "", "bot ID")
	sensorURIPtr := flag.String("sensor", "", "sendor URI")
	flag.Parse()
	globalConfig = config{botID: *botIDPtr, sensorURI: *sensorURIPtr}
	sleepRegexp := regexp.MustCompile("/sleep\\s(\\d+)\\s(min|hours?)")
	updates := getUpdatesChan()
	pool := new(workerPool)
	startConversation := make(map[int]bool)
	for update := range updates {
		if _, ok := startConversation[update.Message.Chat.ID]; ok {
			delete(startConversation, update.Message.Chat.ID)
			limit, err := strconv.Atoi(update.Message.Text)
			if err != nil {
				sendMessage(update.Message.Chat.ID, "Integer expected. Run command again.")
				continue
			}
			if limit <= 0 {
				sendMessage(update.Message.Chat.ID, "Expected value more than 0. Run command again.")
				continue
			}
			if limit > 10000 {
				sendMessage(update.Message.Chat.ID, "Value can`t be more than 10000. Run command again.")
				continue
			}
			sendMessage(update.Message.Chat.ID, "Start watch CO2 less than "+update.Message.Text)
			pool.start(update.Message.Chat.ID, limit)
		} else if update.Message.Text == "/start" {
			sendMessage(update.Message.Chat.ID, "Enter maximum CO2 value")
			startConversation[update.Message.Chat.ID] = true
		} else if update.Message.Text == "/stop" {
			pool.stop(update.Message.Chat.ID)
		} else if update.Message.Text == "/co2" {
			sensorParams := getCO2()
			sendMessage(update.Message.Chat.ID, "CO2 is "+strconv.Itoa(sensorParams.CO2))
		} else if update.Message.Text == "/sleep" {
			sendSleepKeyboard(update.Message.Chat.ID)
		} else if sleepRegexp.MatchString(update.Message.Text) {
			pool.sendMsg(update.Message.Chat.ID, update.Message.Text)
		} else if update.Message.Text == "/help" {
			sendMessage(update.Message.Chat.ID, "Usage:\n"+
				"/start - Monitor the level of co2\n"+
				"/stop - Stop monitoring\n"+
				"/sleep - Disable for a while\n"+
				"/co2 - Show current CO2 value\n"+
				"/help - Show help")
		}
	}
}

func getCO2() sensor {
	response := httpGet(globalConfig.sensorURI)
	var s sensor
	err := json.Unmarshal([]byte(response), &s)
	if err != nil {
		panic(err)
	}
	return s
}

func sendSleepKeyboard(chatID int) string {
	query := url.Values{}
	keyBoard := "{\"keyboard\":[[\"/sleep 15 min\"],[\"/sleep 30 min\"]," +
		"[\"/sleep 1 hour\"],[\"/sleep 2 hour\"], [\"/sleep 5 hour\"]]," +
		"\"one_time_keyboard\":true}"
	query.Set("chat_id", strconv.Itoa(chatID))
	query.Add("reply_markup", keyBoard)
	query.Add("text", "Select sleep time")
	return apiCall("sendMessage", query)
}

func sendMessage(chatID int, text string) string {
	query := url.Values{}
	query.Set("chat_id", strconv.Itoa(chatID))
	query.Add("text", text)
	return apiCall("sendMessage", query)
}

func getUpdates(offset int, limit int, timeout int) string {
	query := url.Values{}
	if offset != 0 {
		query.Add("offset", strconv.Itoa(offset))
	}
	if limit != 0 {
		query.Add("limit", strconv.Itoa(limit))
	}
	if timeout != 0 {
		query.Add("timeout", strconv.Itoa(timeout))
	}
	return apiCall("getUpdates", query)
}

func getUpdatesChan() chan update {
	offset := 0
	ch := make(chan update)
	go func() {
		for {
			updateStr := getUpdates(offset, 0, 60)
			var response apiResponse
			err := json.Unmarshal([]byte(updateStr), &response)
			if err != nil {
				panic(err)
			}
			for _, update := range response.Result {
				if update.UpdateID >= offset {
					offset = update.UpdateID + 1
					ch <- update
				}
			}
		}
	}()
	return ch
}

func apiCall(method string, query url.Values) string {
	return httpGet("https://api.telegram.org/bot" + globalConfig.botID + "/" + method + "?" + query.Encode())
}

func httpGet(url string) string {
	response, err := http.Get(url)
	if err != nil {
		panic(err)
	}
	body, err := ioutil.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		panic(err)
	}
	return string(body)
}
