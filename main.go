package main

import (
   "log"
   "net/http"
   "os"
   "os/signal"
   "syscall"

   tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
   cfg := LoadConfig()

   if cfg.BotToken == "" {
   	log.Fatal("TELEGRAM_BOT_TOKEN is required")
   }

   dbPath := cfg.DatabasePath
   if dbPath == "" {
   	dbPath = "kopeyka.db"
   }

   db, err := NewDB(dbPath)
   if err != nil {
   	log.Fatal("DB error:", err)
   }
   defer db.Close()

   if err := db.Migrate(); err != nil {
   	log.Fatal("Migration error:", err)
   }

   botAPI, err := tgbotapi.NewBotAPI(cfg.BotToken)
   if err != nil {
   	log.Fatal("Bot init error:", err)
   }

   botAPI.Debug = false
   log.Printf("Authorized on account %s", botAPI.Self.UserName)

   bot := NewBot(botAPI, db, cfg)

   // Graceful shutdown
   sigChan := make(chan os.Signal, 1)
   signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

   // Webhook or Long Polling
   if cfg.WebhookURL != "" {
   	wh, _ := tgbotapi.NewWebhook(cfg.WebhookURL + "/" + cfg.BotToken)
   	botAPI.Request(wh)

   	updates := botAPI.ListenForWebhook("/" + cfg.BotToken)
   	go http.ListenAndServe(":"+cfg.WebhookPort, nil)
   	log.Printf("Webhook started on port %s", cfg.WebhookPort)

   	go func() {
   		for update := range updates {
   			bot.HandleUpdate(update)
   		}
   	}()
   } else {
   	u := tgbotapi.NewUpdate(0)
   	u.Timeout = 60
   	updates := botAPI.GetUpdatesChan(u)

   	go func() {
   		for update := range updates {
   			bot.HandleUpdate(update)
   		}
   	}()
   	log.Println("Long polling started")
   }

   <-sigChan
   log.Println("Shutting down gracefully...")
}
