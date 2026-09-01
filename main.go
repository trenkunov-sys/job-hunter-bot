package main

import (
   "context"
   "fmt"
   "log"
   "net/http"
   "os"
   "os/signal"
   "syscall"
   "time"

   tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
   cfg := LoadConfig()

   if cfg.BotToken == "" {
   	log.Fatal("TELEGRAM_BOT_TOKEN is required")
   }

   dbPath := cfg.DatabasePath
   if dbPath == "" {
   	dbPath = "/data/kopeyka.db"
   }

   // Создаём директорию для БД если нужно
   if dir := os.Getenv("DATABASE_DIR"); dir != "" {
   	os.MkdirAll(dir, 0755)
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

   // HTTP mux с health check
   mux := http.NewServeMux()
   mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
   	w.WriteHeader(http.StatusOK)
   	w.Write([]byte("ok"))
   })

   srv := &http.Server{
   	Addr:    ":" + cfg.WebhookPort,
   	Handler: mux,
   }

   sigChan := make(chan os.Signal, 1)
   signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

   updates := make(chan tgbotapi.Update, 100)

   if cfg.WebhookURL != "" {
   	wh, err := tgbotapi.NewWebhook(cfg.WebhookURL + "/" + cfg.BotToken)
   	if err != nil {
   		log.Fatal("Webhook init error:", err)
   	}
   	_, err = botAPI.Request(wh)
   	if err != nil {
   		log.Fatal("Webhook set error:", err)
   	}

   	info, err := botAPI.GetWebhookInfo()
   	if err == nil {
   		log.Printf("Webhook info: %s", info.URL)
   	}

   	// Telegram webhook handler
   	mux.HandleFunc("/"+cfg.BotToken, func(w http.ResponseWriter, r *http.Request) {
   		var update tgbotapi.Update
   		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
   			w.WriteHeader(http.StatusBadRequest)
   			return
   		}
   		updates <- update
   		w.WriteHeader(http.StatusOK)
   		w.Write([]byte("ok"))
   	})

   	go func() {
   		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
   			log.Fatalf("HTTP server error: %v", err)
   		}
   	}()
   	log.Printf("Webhook started on port %s", cfg.WebhookPort)
   } else {
   	u := tgbotapi.NewUpdate(0)
   	u.Timeout = 60
   	rawUpdates := botAPI.GetUpdatesChan(u)

   	go func() {
   		for update := range rawUpdates {
   			updates <- update
   		}
   	}()
   	log.Println("Long polling started")
   }

   // Обработчик updates
   go func() {
   	for update := range updates {
   		bot.HandleUpdate(update)
   	}
   }()

   <-sigChan
   log.Println("Shutting down gracefully...")

   ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
   defer cancel()

   if err := srv.Shutdown(ctx); err != nil {
   	log.Printf("HTTP shutdown error: %v", err)
   }
   db.Close()
   log.Println("Shutdown complete")
}
