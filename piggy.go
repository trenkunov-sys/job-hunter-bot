package main

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func PiggyEmojiProgress(current, target float64) string {
	if target <= 0 {
		return "🐷"
	}
	percent := current / target
	if percent >= 1 {
		return "🐷💰✅"
	}
	if percent >= 0.75 {
		return "🐷💰💰"
	}
	if percent >= 0.5 {
		return "🐷💰"
	}
	if percent >= 0.25 {
		return "🐷"
	}
	return "🐽"
}

func FormatPiggyBank(bank PiggyBank, currency string) string {
	emoji := PiggyEmojiProgress(bank.CurrentAmount, bank.TargetAmount)
	bar := ProgressBar(bank.CurrentAmount, bank.TargetAmount, 12)
	percent := 0.0
	if bank.TargetAmount > 0 {
		percent = (bank.CurrentAmount / bank.TargetAmount) * 100
	}

	text := fmt.Sprintf("%s <b>%s</b>\n%s\n%s / %s (%.0f%%)",
		emoji, bank.Name, bar,
		FormatCurrency(bank.CurrentAmount, currency),
		FormatCurrency(bank.TargetAmount, currency), percent)

	if bank.Deadline != nil {
		days := int(time.Until(*bank.Deadline).Hours() / 24)
		if days > 0 {
			perDay := (bank.TargetAmount - bank.CurrentAmount) / float64(days)
			text += fmt.Sprintf("\nOstalos %d dnej\nNuzhno %s/den", days, FormatCurrency(perDay, currency))
		}
	}

	if bank.IsLocked {
		text += "\nZamorozhena do celi"
	}

	if bank.AutoRule != "" {
		var rule AutoRule
		json.Unmarshal([]byte(bank.AutoRule), &rule)
		switch rule.Type {
		case "fixed":
			text += fmt.Sprintf("\nAvto: +%s %s", FormatCurrency(rule.Amount, currency), rule.Period)
		case "percent_income":
			text += fmt.Sprintf("\nAvto: %.0f%% ot dohoda", rule.Percent)
		case "roundup":
			text += fmt.Sprintf("\nAvto: okruglenie do %.0f", rule.Amount)
		case "penalty":
			text += fmt.Sprintf("\nAvto: shtraf za %s", rule.Category)
		}
	}

	return text
}

func ApplyAutoRules(db *DB, user *User, tx Transaction) []string {
	banks, err := db.GetPiggyBanks(user.ID)
	if err != nil || len(banks) == 0 {
		return nil
	}

	var notifications []string
	for _, bank := range banks {
		if bank.AutoRule == "" {
			continue
		}

		var rule AutoRule
		if err := json.Unmarshal([]byte(bank.AutoRule), &rule); err != nil {
			continue
		}

		var amount float64
		applied := false

		switch rule.Type {
		case "percent_income":
			if tx.Type == "income" {
				amount = tx.Amount * (rule.Percent / 100)
				applied = true
			}
		case "roundup":
			if tx.Type == "expense" {
				multiplier := math.Ceil(tx.Amount / rule.Amount)
				rounded := multiplier * rule.Amount
				amount = rounded - tx.Amount
				if amount > 0 {
					applied = true
				}
			}
		case "penalty":
			if tx.Type == "expense" && tx.Category == rule.Category {
				amount = rule.Amount
				applied = true
			}
		case "fixed":
			continue
		}

		if applied && amount > 0 {
			db.UpdatePiggyBankAmount(bank.ID, amount)
			notifications = append(notifications,
				fmt.Sprintf("Kopilka %s popolnena na %s", bank.Name, FormatCurrency(amount, user.Currency)))
		}
	}

	return notifications
}

func GetPiggyKeyboard(bankID int) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Popolnit", fmt.Sprintf("piggy_add|%d", bankID)),
			tgbotapi.NewInlineKeyboardButtonData("Snjat", fmt.Sprintf("piggy_take|%d", bankID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Pravilo", fmt.Sprintf("piggy_rule|%d", bankID)),
			tgbotapi.NewInlineKeyboardButtonData("Udalit", fmt.Sprintf("piggy_del|%d", bankID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Nazad", "piggy_list"),
		),
	)
}
