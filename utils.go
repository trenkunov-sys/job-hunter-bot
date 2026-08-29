package main

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

type RateLimiter struct {
	mu       sync.RWMutex
	requests map[int64][]time.Time
	window   time.Duration
	max      int
}

func NewRateLimiter(window time.Duration, max int) *RateLimiter {
	return &RateLimiter{
		requests: make(map[int64][]time.Time),
		window:   window,
		max:      max,
	}
}

func (rl *RateLimiter) Allow(chatID int64) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	var valid []time.Time
	for _, t := range rl.requests[chatID] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= rl.max {
		return false
	}

	valid = append(valid, now)
	rl.requests[chatID] = valid
	return true
}

func (rl *RateLimiter) Remaining(chatID int64) int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)
	var count int
	for _, t := range rl.requests[chatID] {
		if t.After(cutoff) {
			count++
		}
	}
	remaining := rl.max - count
	if remaining < 0 {
		return 0
	}
	return remaining
}

func ForecastExpenses(db *DB, userID int) (float64, float64, string) {
	now := time.Now()
	currentMonth := now.Format("2006-01")
	prevMonth := now.AddDate(0, -1, 0).Format("2006-01")
	prevPrevMonth := now.AddDate(0, -2, 0).Format("2006-01")

	current, _ := db.GetMonthlyExpense(userID, currentMonth)
	prev, _ := db.GetMonthlyExpense(userID, prevMonth)
	prevPrev, _ := db.GetMonthlyExpense(userID, prevPrevMonth)

	day := now.Day()
	daysInMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()

	var forecast float64
	if day > 0 {
		dailyRate := current / float64(day)
		forecast = dailyRate * float64(daysInMonth)
	}

	avg := (current + prev + prevPrev) / 3

	var trend string
	if forecast > avg*1.2 {
		trend = "Rashody rastut. Rekomenduem sokratit neobjazatelnye traty."
	} else if forecast < avg*0.8 {
		trend = "Rashody snizhajutsja. Otlitchnaja rabota!"
	} else {
		trend = "Rashody stabilny."
	}

	return forecast, avg, trend
}

func FormatCurrency(amount float64, currency string) string {
	symbol := "r"
	switch currency {
	case "USD":
		symbol = "$"
	case "EUR":
		symbol = "E"
	}
	return fmt.Sprintf("%.2f %s", amount, symbol)
}

func CheckBudgetAlert(db *DB, user *User) (bool, string) {
	if user.MonthlyBudget <= 0 {
		return false, ""
	}

	now := time.Now()
	currentMonth := now.Format("2006-01")
	spent, _ := db.GetMonthlyExpense(user.ID, currentMonth)
	percent := (spent / user.MonthlyBudget) * 100

	if percent >= 100 {
		return true, fmt.Sprintf("Budzhet prevyshen! Potracheno: %s iz %s (%.0f%%)",
			FormatCurrency(spent, user.Currency), FormatCurrency(user.MonthlyBudget, user.Currency), percent)
	}
	if percent >= 80 {
		return true, fmt.Sprintf("Budzhet na 80%% ischerpan! Potracheno: %s iz %s (%.0f%%)",
			FormatCurrency(spent, user.Currency), FormatCurrency(user.MonthlyBudget, user.Currency), percent)
	}
	return false, ""
}

func GenerateReferralLink(botName, code string) string {
	return fmt.Sprintf("https://t.me/%s?start=%s", botName, code)
}

func EmojiForCategory(name string) string {
	m := map[string]string{
		"Produkty": "🛒", "Transport": "🚇", "Zhiljo": "🏠",
		"Kommunka": "💡", "Svjaz": "📱", "Zdorove": "💊",
		"Razvlechenija": "🎬", "Restorany": "🍽", "Odezhda": "👕",
		"Obrazovanie": "📚", "Podarki": "🎁", "Puteshestvija": "✈️",
		"Avtomobil": "🚗", "Deti": "👶", "Pitomcy": "🐾",
		"Nalogi": "📄", "Sberezhenija": "💰", "Investicii": "📈",
		"Drugoe": "📦", "Dohod": "💵",
	}
	if e, ok := m[name]; ok {
		return e
	}
	return "📋"
}

func ProgressBar(current, total float64, width int) string {
	if total <= 0 {
		return strings.Repeat("░", width)
	}
	percent := current / total
	if percent > 1 {
		percent = 1
	}
	if percent < 0 {
		percent = 0
	}
	filled := int(math.Round(float64(width) * percent))
	empty := width - filled
	return strings.Repeat("█", filled) + strings.Repeat("░", empty)
}
