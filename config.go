package main

import (
   "os"
   "strconv"
   "time"

   "github.com/joho/godotenv"
)

type Config struct {
   BotToken          string
   DeepSeekAPIKey    string
   DatabasePath      string
   AdminChatID       int64
   AdminUsername     string
   YooKassaShopID    string
   YooKassaSecretKey string
   StripeSecretKey   string
   WebhookURL        string
   WebhookPort       string
   RateLimitWindow   time.Duration
   RateLimitMax      int
}

func LoadConfig() *Config {
   _ = godotenv.Load()

   adminID, _ := strconv.ParseInt(os.Getenv("ADMIN_CHAT_ID"), 10, 64)
   port := os.Getenv("WEBHOOK_PORT")
   if port == "" {
   	port = "8080"
   }

   rateMax, _ := strconv.Atoi(os.Getenv("RATE_LIMIT_MAX"))
   if rateMax == 0 {
   	rateMax = 30
   }

   rateWindow, _ := strconv.Atoi(os.Getenv("RATE_LIMIT_WINDOW_SEC"))
   if rateWindow == 0 {
   	rateWindow = 60
   }

   return &Config{
   	BotToken:          os.Getenv("TELEGRAM_BOT_TOKEN"),
   	DeepSeekAPIKey:    os.Getenv("DEEPSEEK_API_KEY"),
   	DatabasePath:      getEnv("DATABASE_PATH", "kopeyka.db"),
   	AdminChatID:       adminID,
   	AdminUsername:     getEnv("ADMIN_USERNAME", "Trene4ca"),
   	YooKassaShopID:    os.Getenv("YOOKASSA_SHOP_ID"),
   	YooKassaSecretKey: os.Getenv("YOOKASSA_SECRET_KEY"),
   	StripeSecretKey:   os.Getenv("STRIPE_SECRET_KEY"),
   	WebhookURL:        os.Getenv("WEBHOOK_URL"),
   	WebhookPort:       port,
   	RateLimitWindow:   time.Duration(rateWindow) * time.Second,
   	RateLimitMax:      rateMax,
   }
}

func getEnv(key, fallback string) string {
   if v := os.Getenv(key); v != "" {
   	return v
   }
   return fallback
}
