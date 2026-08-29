package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Bot struct {
	API         *tgbotapi.BotAPI
	DB          *DB
	Config      *Config
	Limiter     *RateLimiter
	BotUsername string
}

func NewBot(api *tgbotapi.BotAPI, db *DB, cfg *Config) *Bot {
	return &Bot{
		API:         api,
		DB:          db,
		Config:      cfg,
		Limiter:     NewRateLimiter(cfg.RateLimitWindow, cfg.RateLimitMax),
		BotUsername: api.Self.UserName,
	}
}

func (b *Bot) HandleUpdate(update tgbotapi.Update) {
	if update.PreCheckoutQuery != nil {
		_, err := b.API.Request(tgbotapi.PreCheckoutConfig{
			PreCheckoutQueryID: update.PreCheckoutQuery.ID,
			OK:                 true,
		})
		if err != nil {
			log.Println("PreCheckout error:", err)
		}
		return
	}

	if update.Message != nil && update.Message.SuccessfulPayment != nil {
		b.handleSuccessfulPayment(update.Message)
		return
	}

	if update.CallbackQuery != nil {
		b.handleCallback(update.CallbackQuery)
		return
	}
	if update.Message == nil {
		return
	}

	msg := update.Message
	chatID := msg.Chat.ID

	if !b.Limiter.Allow(chatID) {
		b.sendMessage(chatID, "Slishkom mnogo zaprosov. Podozhdite nemnogo.")
		return
	}

	user, err := b.DB.GetOrCreateUser(chatID, msg.From.UserName, msg.From.FirstName)
	if err != nil {
		log.Println("Error getting user:", err)
		return
	}

	if msg.IsCommand() && msg.Command() == "start" && msg.CommandArguments() != "" {
		b.handleReferralStart(user, msg)
		return
	}

	state, _ := b.DB.GetUserState(chatID)
	if state.State != "" && !msg.IsCommand() {
		b.handleFSM(user, msg, state)
		return
	}

	if msg.IsCommand() {
		switch msg.Command() {
		case "start":
			b.handleStart(user, msg)
		case "menu":
			b.handleMenu(user, msg)
		case "currency":
			b.handleCurrency(user, msg)
		case "budget":
			b.handleBudget(user, msg)
		case "add":
			b.handleAddMenu(user, msg)
		case "add_expense":
			b.handleAddExpense(user, msg)
		case "add_income":
			b.handleAddIncome(user, msg)
		case "today", "week", "month":
			b.handleReport(user, msg, msg.Command())
		case "balance":
			b.handleBalance(user, msg)
		case "categories":
			b.handleCategories(user, msg)
		case "advice":
			b.handleAdvice(user, msg)
		case "family":
			b.handleFamily(user, msg)
		case "invite":
			b.handleInvite(user, msg)
		case "export":
			b.handleExport(user, msg)
		case "analytics":
			b.handleAnalytics(user, msg)
		case "goals":
			b.handleGoals(user, msg)
		case "forecast":
			b.handleForecast(user, msg)
		case "referral":
			b.handleReferral(user, msg)
		case "upgrade":
			b.handleUpgrade(user, msg)
		case "pay":
			b.handlePay(user, msg)
		case "piggy", "jar", "copilka":
			b.handlePiggy(user, msg)
		case "team":
			b.handleTeam(user, msg)
		case "shared_budget":
			b.handleSharedBudget(user, msg)
		case "reports":
			b.handleTeamReports(user, msg)
		case "help":
			b.handleHelp(user, msg)
		case "support":
			b.handleSupport(user, msg)
		default:
			b.sendMessage(chatID, "Neizvestnaja komanda. Ispolzujte /menu ili /help")
		}
		return
	}

	if msg.Text != "" {
		b.handleQuickAdd(user, msg)
	}
}

func (b *Bot) sendMessage(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"
	msg.DisableWebPagePreview = true
	b.API.Send(msg)
}

func (b *Bot) sendMessageWithKeyboard(chatID int64, text string, keyboard interface{}) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"
	msg.DisableWebPagePreview = true
	msg.ReplyMarkup = keyboard
	b.API.Send(msg)
}

func (b *Bot) editMessageText(chatID int64, messageID int, text string, keyboard interface{}) {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.ParseMode = "HTML"
	if keyboard != nil {
		edit.ReplyMarkup = keyboard.(*tgbotapi.InlineKeyboardMarkup)
	}
	b.API.Send(edit)
}

func (b *Bot) deleteMessage(chatID int64, messageID int) {
	b.API.Send(tgbotapi.NewDeleteMessage(chatID, messageID))
}

func mainMenuKeyboard(plan string) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Dobavit", "menu_add"),
		tgbotapi.NewInlineKeyboardButtonData("Otcheti", "menu_reports"),
	))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Balans", "menu_balance"),
		tgbotapi.NewInlineKeyboardButtonData("Kopilki", "menu_piggy"),
	))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Celi", "menu_goals"),
		tgbotapi.NewInlineKeyboardButtonData("Prognoz", "menu_forecast"),
	))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("AI-Sovet", "menu_advice"),
		tgbotapi.NewInlineKeyboardButtonData("Pomosch", "menu_support"),
	))
	if plan != "free" {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Semja", "menu_family"),
			tgbotapi.NewInlineKeyboardButtonData("Export", "menu_export"),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Tarify", "menu_upgrade"),
		tgbotapi.NewInlineKeyboardButtonData("Komandy", "menu_help"),
	))
	return tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func categoryKeyboard(action string) tgbotapi.InlineKeyboardMarkup {
	cats := []struct{ name, emoji string }{
		{"Produkty", "🛒"}, {"Transport", "🚇"}, {"Zhiljo", "🏠"},
		{"Kommunka", "💡"}, {"Svjaz", "📱"}, {"Zdorove", "💊"},
		{"Razvlechenija", "🎬"}, {"Restorany", "🍽"}, {"Odezhda", "👕"},
		{"Obrazovanie", "📚"}, {"Podarki", "🎁"}, {"Puteshestvija", "✈️"},
		{"Avtomobil", "🚗"}, {"Deti", "👶"}, {"Pitomcy", "🐾"},
		{"Nalogi", "📄"}, {"Sberezhenija", "💰"}, {"Investicii", "📈"},
		{"Drugoe", "📦"},
	}
	var rows [][]tgbotapi.InlineKeyboardButton
	for i := 0; i < len(cats); i += 3 {
		var row []tgbotapi.InlineKeyboardButton
		for j := i; j < i+3 && j < len(cats); j++ {
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(
				cats[j].emoji+" "+cats[j].name,
				fmt.Sprintf("%s|%s", action, cats[j].name),
			))
		}
		rows = append(rows, row)
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Nazad", "menu_main"),
	))
	return tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func currencyKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Rubl", "currency|RUB"),
			tgbotapi.NewInlineKeyboardButtonData("Dollar", "currency|USD"),
			tgbotapi.NewInlineKeyboardButtonData("Evro", "currency|EUR"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Nazad", "menu_settings"),
		),
	)
}

func reportPeriodKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Segodnja", "report|today"),
			tgbotapi.NewInlineKeyboardButtonData("Nedelja", "report|week"),
			tgbotapi.NewInlineKeyboardButtonData("Mesjac", "report|month"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Nazad", "menu_main"),
		),
	)
}

func upgradeKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Premium 200r", "upgrade|premium"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Business 700r", "upgrade|business"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Nazad", "menu_main"),
		),
	)
}

func addMenuKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Rashod", "add|expense"),
			tgbotapi.NewInlineKeyboardButtonData("Dohod", "add|income"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Nazad", "menu_main"),
		),
	)
}

func piggyListKeyboard(banks []PiggyBank) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, bank := range banks {
		emoji := PiggyEmojiProgress(bank.CurrentAmount, bank.TargetAmount)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("%s %s", emoji, bank.Name),
				fmt.Sprintf("piggy_view|%d", bank.ID),
			),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Novaja kopilka", "piggy_create"),
	))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Nazad", "menu_main"),
	))
	return tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func autoRuleKeyboard(bankID int) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Procent ot dohoda", fmt.Sprintf("rule|percent|%d", bankID)),
			tgbotapi.NewInlineKeyboardButtonData("Okruglenie", fmt.Sprintf("rule|roundup|%d", bankID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Shtraf", fmt.Sprintf("rule|penalty|%d", bankID)),
			tgbotapi.NewInlineKeyboardButtonData("Fiksirovanno", fmt.Sprintf("rule|fixed|%d", bankID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Bez pravila", fmt.Sprintf("rule|none|%d", bankID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Nazad", fmt.Sprintf("piggy_view|%d", bankID)),
		),
	)
}
func (b *Bot) handleStart(user *User, msg *tgbotapi.Message) {
	if user.Currency == "RUB" && user.MonthlyBudget == 0 {
		b.DB.SetUserState(msg.Chat.ID, "onboarding_currency", "")
		b.sendMessageWithKeyboard(msg.Chat.ID,
			"Dobro pozhalovat v Kopejku!\n\nJa pomogu vesti uchet finansov, kopit dengi i dostigat celej.\n\nDavajte nastroit vash akkaunt. Vyberite valjutu:",
			currencyKeyboard())
		return
	}

	text := fmt.Sprintf("Privet, %s!\n\nVash finansovyj assistent gotov.\nTarif: <b>%s</b>\n\nIspolzujte /menu dlja navigacii.", user.FirstName, planName(user.Plan))
	b.sendMessageWithKeyboard(msg.Chat.ID, text, mainMenuKeyboard(user.Plan))
}

func (b *Bot) handleMenu(user *User, msg *tgbotapi.Message) {
	text := fmt.Sprintf("Glavnoe menju\n\nTarif: %s | Valjuta: %s", planName(user.Plan), user.Currency)
	b.sendMessageWithKeyboard(msg.Chat.ID, text, mainMenuKeyboard(user.Plan))
}

func (b *Bot) handleCurrency(user *User, msg *tgbotapi.Message) {
	b.sendMessageWithKeyboard(msg.Chat.ID, "Vyberite valjutu:", currencyKeyboard())
}

func (b *Bot) handleBudget(user *User, msg *tgbotapi.Message) {
	args := msg.CommandArguments()
	if args == "" {
		b.DB.SetUserState(msg.Chat.ID, "set_budget", "")
		b.sendMessage(msg.Chat.ID, "Vvedite mesjachnyj budzhet (tolko chislo):")
		return
	}
	amount, err := strconv.ParseFloat(args, 64)
	if err != nil {
		b.sendMessage(msg.Chat.ID, "Vvedite korrektnoe chislo. Primer: /budget 50000")
		return
	}
	b.DB.UpdateUserBudget(user.ChatID, amount)
	b.sendMessage(msg.Chat.ID, fmt.Sprintf("Budzhet ustanovlen: %s", FormatCurrency(amount, user.Currency)))
}

func (b *Bot) handleAddMenu(user *User, msg *tgbotapi.Message) {
	b.sendMessageWithKeyboard(msg.Chat.ID, "Chto dobavit?", addMenuKeyboard())
}

func (b *Bot) handleAddExpense(user *User, msg *tgbotapi.Message) {
	if user.Plan == "free" {
		now := time.Now().Format("2006-01")
		count, _ := b.DB.CountTransactions(user.ID, now)
		if count >= 50 {
			b.sendMessageWithKeyboard(msg.Chat.ID,
				"Limit ischerpan\n\nVy ispolzovali 50 iz 50 transakcij v etom mesjace.\nPerejdite na Premium za 200 r/mes dlja bezlimita.",
				upgradeKeyboard())
			return
		}
	}
	b.DB.SetUserState(msg.Chat.ID, "add_expense_amount", "")
	b.sendMessage(msg.Chat.ID, "Novyj rashod\n\nVvedite summu:")
}

func (b *Bot) handleAddIncome(user *User, msg *tgbotapi.Message) {
	b.DB.SetUserState(msg.Chat.ID, "add_income_amount", "")
	b.sendMessage(msg.Chat.ID, "Novyj dohod\n\nVvedite summu:")
}

func (b *Bot) handleQuickAdd(user *User, msg *tgbotapi.Message) {
	parts := strings.Fields(msg.Text)
	if len(parts) < 2 {
		b.sendMessage(msg.Chat.ID, "Format: <summa> <kategorija>\nPrimer: 500 produkty")
		return
	}

	amount, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		b.sendMessage(msg.Chat.ID, "Snachala ukazhite summu. Primer: 500 produkty")
		return
	}

	if user.Plan == "free" {
		now := time.Now().Format("2006-01")
		count, _ := b.DB.CountTransactions(user.ID, now)
		if count >= 50 {
			b.sendMessageWithKeyboard(msg.Chat.ID,
				"Limit ischerpan\n\n50/50 transakcij v etom mesjace.\nPerejdite na Premium za 200 r/mes.",
				upgradeKeyboard())
			return
		}
	}

	category := parts[1]
	description := ""
	if len(parts) > 2 {
		description = strings.Join(parts[2:], " ")
	}

	var familyID *int
	if user.FamilyID != nil {
		familyID = user.FamilyID
	}

	tx := Transaction{UserID: user.ID, FamilyID: familyID, Type: "expense", Amount: amount, Category: category, Description: description}
	err = b.DB.AddTransaction(user.ID, familyID, "expense", amount, category, description)
	if err != nil {
		b.sendMessage(msg.Chat.ID, "Oshibka sohranenija")
		return
	}

	emoji := EmojiForCategory(category)
	text := fmt.Sprintf("Sohraneno\n\n%s %s\n%s", emoji, category, FormatCurrency(amount, user.Currency))
	if description != "" {
		text += fmt.Sprintf("\n%s", description)
	}

	notifications := ApplyAutoRules(b.DB, user, tx)
	for _, n := range notifications {
		text += "\n\n" + n
	}

	if alert, msgAlert := CheckBudgetAlert(b.DB, user); alert {
		text += "\n\n" + msgAlert
	} else if user.MonthlyBudget > 0 && amount > user.MonthlyBudget*0.15 {
		text += fmt.Sprintf("\n\nEtot rashod — %.0f%% ot vashego budzheta.", (amount/user.MonthlyBudget)*100)
	}

	b.sendMessageWithKeyboard(msg.Chat.ID, text, mainMenuKeyboard(user.Plan))
}

func (b *Bot) handleReport(user *User, msg *tgbotapi.Message, period string) {
	txs, err := b.DB.GetTransactionsByPeriod(user.ID, period)
	if err != nil {
		b.sendMessage(msg.Chat.ID, "Oshibka poluchenija otcheta")
		return
	}

	if len(txs) == 0 {
		b.sendMessage(msg.Chat.ID, "Net transakcij za etot period")
		return
	}

	var totalExpense, totalIncome float64
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Otchjet za %s\n\n", periodName(period)))

	for _, tx := range txs {
		icon := "-"
		if tx.Type == "income" {
			icon = "+"
			totalIncome += tx.Amount
		} else {
			totalExpense += tx.Amount
		}
		emoji := EmojiForCategory(tx.Category)
		sb.WriteString(fmt.Sprintf("%s %s %.0f — %s %s\n", icon, emoji, tx.Amount, tx.Category, tx.CreatedAt.Format("02.01")))
	}

	sb.WriteString(fmt.Sprintf("\nItogo:\nRashody: %s\nDohody: %s\nBalans: %s",
		FormatCurrency(totalExpense, user.Currency),
		FormatCurrency(totalIncome, user.Currency),
		FormatCurrency(totalIncome-totalExpense, user.Currency)))

	b.sendMessageWithKeyboard(msg.Chat.ID, sb.String(), mainMenuKeyboard(user.Plan))
}

func (b *Bot) handleBalance(user *User, msg *tgbotapi.Message) {
	balance, err := b.DB.GetBalance(user.ID)
	if err != nil {
		b.sendMessage(msg.Chat.ID, "Oshibka")
		return
	}

	savings, _ := b.DB.GetTotalPiggySavings(user.ID)

	text := fmt.Sprintf("Tekuschij balans\n\n%s", FormatCurrency(balance, user.Currency))
	if savings > 0 {
		text += fmt.Sprintf("\nV kopilkah: %s", FormatCurrency(savings, user.Currency))
	}
	if user.MonthlyBudget > 0 {
		now := time.Now()
		currentMonth := now.Format("2006-01")
		spent, _ := b.DB.GetMonthlyExpense(user.ID, currentMonth)
		remaining := user.MonthlyBudget - spent
		percent := (spent / user.MonthlyBudget) * 100
		bar := ProgressBar(spent, user.MonthlyBudget, 10)

		text += fmt.Sprintf("\n\nBudzhet na %s\n%s\nPotracheno: %s (%.0f%%)\nOstalos: %s",
			now.Format("01.2006"), bar,
			FormatCurrency(spent, user.Currency), percent,
			FormatCurrency(remaining, user.Currency))
	}

	b.sendMessageWithKeyboard(msg.Chat.ID, text, mainMenuKeyboard(user.Plan))
}

func (b *Bot) handleCategories(user *User, msg *tgbotapi.Message) {
	stats, err := b.DB.GetCategoryStats(user.ID, "month")
	if err != nil {
		b.sendMessage(msg.Chat.ID, "Oshibka")
		return
	}

	if len(stats) == 0 {
		b.sendMessage(msg.Chat.ID, "Net dannyh za mesjac")
		return
	}

	var sb strings.Builder
	sb.WriteString("Rashody po kategorijam (mesjac)\n\n")
	for cat, amount := range stats {
		emoji := EmojiForCategory(cat)
		sb.WriteString(fmt.Sprintf("%s %s: %s\n", emoji, cat, FormatCurrency(amount, user.Currency)))
	}
	b.sendMessageWithKeyboard(msg.Chat.ID, sb.String(), mainMenuKeyboard(user.Plan))
}

func (b *Bot) handleAdvice(user *User, msg *tgbotapi.Message) {
	if user.Plan == "free" {
		b.sendMessageWithKeyboard(msg.Chat.ID,
			"AI-sovety dostupny v Premium.\n\nPoluchite personalnye rekomendacii po ekonomii za 200 r/mes.",
			upgradeKeyboard())
		return
	}

	b.sendMessage(msg.Chat.ID, "Analiziruju vashi finansy...")
	advice, err := GetAIAdvice(b.DB, user.ID, b.Config.DeepSeekAPIKey)
	if err != nil {
		b.sendMessage(msg.Chat.ID, "Oshibka AI. Poprobujte pozhe.")
		return
	}

	b.sendMessageWithKeyboard(msg.Chat.ID, "AI-sovet:\n\n"+advice, mainMenuKeyboard(user.Plan))
}

func (b *Bot) handleFamily(user *User, msg *tgbotapi.Message) {
	if user.Plan == "free" {
		b.sendMessageWithKeyboard(msg.Chat.ID,
			"Semejnyj dostup v Premium (200 r/mes) ili Business (700 r/mes).",
			upgradeKeyboard())
		return
	}

	if msg.CommandArguments() == "" {
		if user.FamilyID != nil {
			family, _ := b.DB.GetFamily(*user.FamilyID)
			members, _ := b.DB.GetFamilyMembers(*user.FamilyID)
			text := fmt.Sprintf("%s\n\nUchastniki (%d/%d):\n", family.Name, len(members), familyLimit(user.Plan))
			for _, m := range members {
				role := "U"
				if m.ID == family.OwnerID {
					role = "V"
				}
				text += fmt.Sprintf("%s %s (@%s)\n", role, m.FirstName, m.Username)
			}
			text += "\nPriglasit: /invite @username"
			b.sendMessageWithKeyboard(msg.Chat.ID, text, mainMenuKeyboard(user.Plan))
		} else {
			b.DB.SetUserState(msg.Chat.ID, "create_family", "")
			b.sendMessage(msg.Chat.ID, "Vvedite nazvanie semji/komandy:")
		}
		return
	}

	if user.FamilyID != nil {
		b.sendMessage(msg.Chat.ID, "Vy uzhe v semje")
		return
	}

	name := msg.CommandArguments()
	family, err := b.DB.CreateFamily(name, user.ID)
	if err != nil {
		b.sendMessage(msg.Chat.ID, "Oshibka: "+err.Error())
		return
	}

	b.sendMessage(msg.Chat.ID, fmt.Sprintf("Semja %s sozdana!\nPriglasite uchastnikov: /invite @username", family.Name))
}

func (b *Bot) handleInvite(user *User, msg *tgbotapi.Message) {
	if user.FamilyID == nil {
		b.sendMessage(msg.Chat.ID, "Snachala sozdaite semju: /family Nazvanie")
		return
	}

	family, _ := b.DB.GetFamily(*user.FamilyID)
	if family.OwnerID != user.ID {
		b.sendMessage(msg.Chat.ID, "Tolko vladelec mozhet priglashat")
		return
	}

	members, _ := b.DB.GetFamilyMembers(*user.FamilyID)
	limit := familyLimit(user.Plan)
	if len(members) >= limit {
		b.sendMessage(msg.Chat.ID, fmt.Sprintf("Limit uchastnikov (%d) dostignut", limit))
		return
	}

	b.DB.SetUserState(msg.Chat.ID, "invite_member", fmt.Sprintf("%d", *user.FamilyID))
	b.sendMessage(msg.Chat.ID, "Vvedite @username polzovatelja dlja priglashenija:")
}

func (b *Bot) handleExport(user *User, msg *tgbotapi.Message) {
	if user.Plan == "free" {
		b.sendMessageWithKeyboard(msg.Chat.ID,
			"Export v CSV dostupen v Premium za 200 r/mes.",
			upgradeKeyboard())
		return
	}

	path, err := ExportToCSV(b.DB, user.ID)
	if err != nil {
		b.sendMessage(msg.Chat.ID, "Oshibka exporta")
		return
	}

	file := tgbotapi.NewDocument(msg.Chat.ID, tgbotapi.FilePath(path))
	file.Caption = "Vashi transakcii"
	b.API.Send(file)
}

func (b *Bot) handleAnalytics(user *User, msg *tgbotapi.Message) {
	if user.Plan == "free" {
		b.sendMessageWithKeyboard(msg.Chat.ID,
			"Analitika i grafiki v Premium za 200 r/mes.",
			upgradeKeyboard())
		return
	}

	stats, _ := b.DB.GetCategoryStats(user.ID, "month")
	if len(stats) == 0 {
		b.sendMessage(msg.Chat.ID, "Net dannyh")
		return
	}

	chartURL := generateChartURL(stats)
	photo := tgbotapi.NewPhoto(msg.Chat.ID, tgbotapi.FileURL(chartURL))
	photo.Caption = "Analitika rashodov"
	b.API.Send(photo)

	var sb strings.Builder
	sb.WriteString("Top kategorij:\n")
	i := 0
	for cat, amount := range stats {
		if i >= 5 {
			break
		}
		emoji := EmojiForCategory(cat)
		sb.WriteString(fmt.Sprintf("%d. %s %s: %s\n", i+1, emoji, cat, FormatCurrency(amount, user.Currency)))
		i++
	}
	b.sendMessageWithKeyboard(msg.Chat.ID, sb.String(), mainMenuKeyboard(user.Plan))
}

func (b *Bot) handleGoals(user *User, msg *tgbotapi.Message) {
	args := msg.CommandArguments()
	if args == "" {
		goals, err := b.DB.GetGoals(user.ID)
		if err != nil || len(goals) == 0 {
			b.DB.SetUserState(msg.Chat.ID, "goal_name", "")
			b.sendMessage(msg.Chat.ID, "Novaja cel\n\nVvedite nazvanie:")
			return
		}

		var sb strings.Builder
		sb.WriteString("Vashi celi:\n\n")
		for _, g := range goals {
			percent := 0.0
			if g.TargetAmount > 0 {
				percent = (g.CurrentAmount / g.TargetAmount) * 100
			}
			bar := ProgressBar(g.CurrentAmount, g.TargetAmount, 10)
			sb.WriteString(fmt.Sprintf("%s\n%s\n%.0f%% — %s / %s\n\n",
				g.Name, bar, percent,
				FormatCurrency(g.CurrentAmount, user.Currency),
				FormatCurrency(g.TargetAmount, user.Currency)))
		}
		sb.WriteString("Sozdat novuju: /goals Nazvanie Summa")
		b.sendMessageWithKeyboard(msg.Chat.ID, sb.String(), mainMenuKeyboard(user.Plan))
		return
	}

	parts := strings.Fields(args)
	if len(parts) < 2 {
		b.sendMessage(msg.Chat.ID, "Format: /goals Nazvanie 100000")
		return
	}

	target, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil {
		b.sendMessage(msg.Chat.ID, "Ukazhite celevuju summu v konce")
		return
	}

	name := strings.Join(parts[:len(parts)-1], " ")
	var familyID *int
	if user.FamilyID != nil {
		familyID = user.FamilyID
	}

	err = b.DB.CreateGoal(user.ID, familyID, name, target, "")
	if err != nil {
		b.sendMessage(msg.Chat.ID, "Oshibka")
		return
	}

	monthly := target / 12
	b.sendMessageWithKeyboard(msg.Chat.ID,
		fmt.Sprintf("Cel %s sozdana!\nCel: %s\nOtkladyvajte %s/mes",
			name, FormatCurrency(target, user.Currency), FormatCurrency(monthly, user.Currency)),
		mainMenuKeyboard(user.Plan))
}

func (b *Bot) handleForecast(user *User, msg *tgbotapi.Message) {
	forecast, avg, trend := ForecastExpenses(b.DB, user.ID)

	var sb strings.Builder
	sb.WriteString("Prognoz na konets mesjaca\n\n")
	sb.WriteString(fmt.Sprintf("Prognoz: %s\n", FormatCurrency(forecast, user.Currency)))
	sb.WriteString(fmt.Sprintf("Srednee (3 mes): %s\n", FormatCurrency(avg, user.Currency)))
	sb.WriteString(fmt.Sprintf("\n%s", trend))

	if user.MonthlyBudget > 0 {
		now := time.Now()
		currentMonth := now.Format("2006-01")
		spent, _ := b.DB.GetMonthlyExpense(user.ID, currentMonth)
		remaining := user.MonthlyBudget - spent
		if forecast > user.MonthlyBudget {
			overrun := forecast - user.MonthlyBudget
			sb.WriteString(fmt.Sprintf("\n\nVnimanije!\nPrognoz prevyshaet budzhet na %s", FormatCurrency(overrun, user.Currency)))
		} else if remaining > 0 {
			sb.WriteString(fmt.Sprintf("\n\nV ramkah budzheta. Ostatok prognoziruetsja: %s", FormatCurrency(remaining, user.Currency)))
		}
	}

	b.sendMessageWithKeyboard(msg.Chat.ID, sb.String(), mainMenuKeyboard(user.Plan))
}

func (b *Bot) handleReferral(user *User, msg *tgbotapi.Message) {
	link := GenerateReferralLink(b.BotUsername, user.ReferralCode)
	bonus := ""
	if user.ReferralCount > 0 {
		bonus = fmt.Sprintf("\n\nPriglasheno: %d", user.ReferralCount)
	}
	b.sendMessage(msg.Chat.ID,
		fmt.Sprintf("Priglasite druzej\n\nZa kazhdogo druza, oformivshego Premium, vy poluchaete +7 dnej besplatno!\n\nVasha ssylka:\n%s%s", link, bonus))
}

func (b *Bot) handleUpgrade(user *User, msg *tgbotapi.Message) {
	text := "Tarify Kopejki\n\nBesplatnyj — 0 r\n• 50 transakcij/mes\n• Bazovye kategorii\n• 1 akkaunt\n\nPremium — 200 r/mes\n• Bezlimit transakcij\n• AI-sovety\n• Semejnyj dostup (do 5)\n• Export CSV\n• Analitika i grafiki\n• Finansovye celi\n• Kopilki s avtopopolneniem\n\nBusiness — 700 r/mes\n• Do 20 polzovatelej\n• Obshij budzhet\n• Komandnye otchety\n• Prioritetnaja podderzhka\n\nVyberite tarif:"
	b.sendMessageWithKeyboard(msg.Chat.ID, text, upgradeKeyboard())
}

func (b *Bot) handlePay(user *User, msg *tgbotapi.Message) {
	args := msg.CommandArguments()
	prices := []tgbotapi.LabeledPrice{}
	var title, description string

	switch args {
	case "premium":
		title = "Premium — 1 mesjac"
		description = "Bezlimit, AI, kopilki, export, analitika"
		prices = append(prices, tgbotapi.LabeledPrice{Label: "Premium", Amount: 20000})
	case "business":
		title = "Business — 1 mesjac"
		description = "Do 20 polzovatelej, komandnye otchety"
		prices = append(prices, tgbotapi.LabeledPrice{Label: "Business", Amount: 70000})
	default:
		b.sendMessage(msg.Chat.ID, "Ispolzujte:\n/pay premium — 200 r\n/pay business — 700 r")
		return
	}

	invoice := tgbotapi.NewInvoice(
		msg.Chat.ID,
		title,
		description,
		args,
		"",
		"",
		"RUB",
		prices,
	)
	b.API.Send(invoice)
}

func (b *Bot) handleSuccessfulPayment(msg *tgbotapi.Message) {
	payload := msg.SuccessfulPayment.InvoicePayload
	user, _ := b.DB.GetOrCreateUser(msg.Chat.ID, msg.From.UserName, msg.From.FirstName)

	switch payload {
	case "premium":
		b.DB.UpdateUserPlan(user.ID, "premium")
		b.sendMessage(msg.Chat.ID, "Premium aktivirovan!\n\nDobro pozhalovat v bezlimitnyj mir Kopejki.")
	case "business":
		b.DB.UpdateUserPlan(user.ID, "business")
		b.sendMessage(msg.Chat.ID, "Business aktivirovan!\n\nUpravljajte komandoj i obshim budzhetom.")
	}

	if b.Config.AdminChatID != 0 {
		b.sendMessage(b.Config.AdminChatID,
			fmt.Sprintf("Novaja oplata!\n\nPolzovatel: %s (@%s)\nTarif: %s\nSumma: %d",
				user.FirstName, user.Username, payload, msg.SuccessfulPayment.TotalAmount))
	}
}

func (b *Bot) handlePiggy(user *User, msg *tgbotapi.Message) {
	args := msg.CommandArguments()

	if args == "" {
		banks, err := b.DB.GetPiggyBanks(user.ID)
		if err != nil || len(banks) == 0 {
			b.DB.SetUserState(msg.Chat.ID, "piggy_name", "")
			b.sendMessage(msg.Chat.ID, "Kopilki\n\nU vas poka net kopilok. Vvedite nazvanie novoj:")
			return
		}

		var sb strings.Builder
		sb.WriteString("Vashi kopilki\n\n")
		totalSaved, _ := b.DB.GetTotalPiggySavings(user.ID)
		sb.WriteString(fmt.Sprintf("Vsego nakopleno: %s\n\n", FormatCurrency(totalSaved, user.Currency)))

		for _, bank := range banks {
			sb.WriteString(FormatPiggyBank(bank, user.Currency) + "\n\n")
		}

		b.sendMessageWithKeyboard(msg.Chat.ID, sb.String(), piggyListKeyboard(banks))
		return
	}

	parts := strings.Fields(args)
	if len(parts) < 2 {
		b.sendMessage(msg.Chat.ID, "Format: /piggy Nazvanie 10000")
		return
	}

	target, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil {
		b.sendMessage(msg.Chat.ID, "Ukazhite celevuju summu v konce")
		return
	}

	name := strings.Join(parts[:len(parts)-1], " ")
	var familyID *int
	if user.FamilyID != nil {
		familyID = user.FamilyID
	}

	id, err := b.DB.CreatePiggyBank(user.ID, familyID, name, target, "🐷", "#FF6B6B", "", false, "")
	if err != nil {
		b.sendMessage(msg.Chat.ID, "Oshibka sozdanija kopilki")
		return
	}

	b.sendMessageWithKeyboard(msg.Chat.ID,
		fmt.Sprintf("Kopilka %s sozdana!\nCel: %s\n\nIspolzujte /piggy dlja upravlenija.", name, FormatCurrency(target, user.Currency)),
		GetPiggyKeyboard(id))
}

func (b *Bot) handleTeam(user *User, msg *tgbotapi.Message) {
	if user.Plan != "business" {
		b.sendMessageWithKeyboard(msg.Chat.ID,
			"Upravlenie komandoj v Business za 700 r/mes.",
			upgradeKeyboard())
		return
	}
	b.handleFamily(user, msg)
}

func (b *Bot) handleSharedBudget(user *User, msg *tgbotapi.Message) {
	if user.FamilyID == nil {
		b.sendMessage(msg.Chat.ID, "Snachala sozdaite komandu")
		return
	}

	members, _ := b.DB.GetFamilyMembers(*user.FamilyID)
	var total float64
	for _, m := range members {
		bal, _ := b.DB.GetBalance(m.ID)
		total += bal
	}

	b.sendMessageWithKeyboard(msg.Chat.ID,
		fmt.Sprintf("Obshij budzhet komandy\n%s\nUchastnikov: %d",
			FormatCurrency(total, user.Currency), len(members)),
		mainMenuKeyboard(user.Plan))
}

func (b *Bot) handleTeamReports(user *User, msg *tgbotapi.Message) {
	if user.Plan != "business" {
		b.sendMessageWithKeyboard(msg.Chat.ID,
			"Dostupno v Business za 700 r/mes.",
			upgradeKeyboard())
		return
	}
	if user.FamilyID == nil {
		b.sendMessage(msg.Chat.ID, "Snachala sozdaite komandu")
		return
	}

	members, _ := b.DB.GetFamilyMembers(*user.FamilyID)
	var sb strings.Builder
	sb.WriteString("Otchjet komandy\n\n")
	for _, m := range members {
		bal, _ := b.DB.GetBalance(m.ID)
		sb.WriteString(fmt.Sprintf("• %s: %s\n", m.FirstName, FormatCurrency(bal, user.Currency)))
	}
	b.sendMessageWithKeyboard(msg.Chat.ID, sb.String(), mainMenuKeyboard(user.Plan))
}

func (b *Bot) handleSupport(user *User, msg *tgbotapi.Message) {
	text := fmt.Sprintf("Pomosch\n\nEsli vy ne ponimaete, chto delat posle onbordinga, ili u vas est voprosy:\n\n1. Najmite /menu — tam vse knopki\n2. Pishite 500 produkty dlja bystrogo dobavlenija rashoda\n3. Ispolzujte /help dlja spiska komand\n\nLichnaja pomosch:\nNapishite @%s — ja (vladelec) otvechu lichno i pomogu razobratsja.\n\nChastye voprosy:\nKak nakopit? — Sozdaite kopilku /piggy i nastrajte avtopopolnenie\nKak priglasit semju? — /family Nazvanie, zatem /invite @username\nKak oplatit? — /pay premium ili /pay business\n\nUdachi!", b.Config.AdminUsername)

	b.sendMessageWithKeyboard(msg.Chat.ID, text, mainMenuKeyboard(user.Plan))
}

func (b *Bot) handleHelp(user *User, msg *tgbotapi.Message) {
	text := fmt.Sprintf("Komandy Kopejki\n\nOsnovnye:\n/menu — glavnoe menju\n/add — bystroe dobavlenie\n/currency — valjuta\n/budget — mesjachnyj budzhet\n/balance — balans + kopilki\n/forecast — prognoz rashodov\n/referral — priglasit druza\n/piggy — kopilki i avtosberezhenija\n\nOtchety:\n/today /week /month — periody\n/categories — po kategorijam\n/analytics — grafiki\n\nPremium (200 r/mes):\n/advice — AI-sovet\n/family — semejnyj dostup\n/export — export CSV\n/goals — finansovye celi\n/pay premium — oplatit Stars\n\nBusiness (700 r/mes):\n/team — upravlenie komandoj\n/shared_budget — obshij budzhet\n/reports — otchety komandy\n/pay business — oplatit Stars\n\nPomosch:\n/support — svjaz s @%s\n/help — eta spravka\n\nBystroe dobavlenie: 500 produkty", b.Config.AdminUsername)
	b.sendMessageWithKeyboard(msg.Chat.ID, text, mainMenuKeyboard(user.Plan))
}
func (b *Bot) handleCallback(query *tgbotapi.CallbackQuery) {
	chatID := query.Message.Chat.ID
	messageID := query.Message.MessageID
	data := query.Data

	user, _ := b.DB.GetOrCreateUser(chatID, query.From.UserName, query.From.FirstName)

	parts := strings.SplitN(data, "|", 3)
	action := parts[0]

	switch action {
	case "menu_main":
		text := fmt.Sprintf("Glavnoe menju\n\nTarif: %s | Valjuta: %s", planName(user.Plan), user.Currency)
		b.editMessageText(chatID, messageID, text, mainMenuKeyboard(user.Plan))

	case "menu_add":
		b.editMessageText(chatID, messageID, "Chto dobavit?", addMenuKeyboard())

	case "menu_reports":
		b.editMessageText(chatID, messageID, "Vyberite period:", reportPeriodKeyboard())

	case "menu_balance":
		balance, _ := b.DB.GetBalance(user.ID)
		text := fmt.Sprintf("Balans\n\n%s", FormatCurrency(balance, user.Currency))
		b.editMessageText(chatID, messageID, text, mainMenuKeyboard(user.Plan))

	case "menu_goals":
		b.deleteMessage(chatID, messageID)
		b.handleGoals(user, &tgbotapi.Message{Chat: query.Message.Chat})

	case "menu_advice":
		b.deleteMessage(chatID, messageID)
		b.handleAdvice(user, &tgbotapi.Message{Chat: query.Message.Chat})

	case "menu_forecast":
		b.deleteMessage(chatID, messageID)
		b.handleForecast(user, &tgbotapi.Message{Chat: query.Message.Chat})

	case "menu_family":
		b.deleteMessage(chatID, messageID)
		b.handleFamily(user, &tgbotapi.Message{Chat: query.Message.Chat, Text: ""})

	case "menu_export":
		b.deleteMessage(chatID, messageID)
		b.handleExport(user, &tgbotapi.Message{Chat: query.Message.Chat})

	case "menu_upgrade":
		b.editMessageText(chatID, messageID, "Vyberite tarif:", upgradeKeyboard())

	case "menu_help":
		b.deleteMessage(chatID, messageID)
		b.handleHelp(user, &tgbotapi.Message{Chat: query.Message.Chat})

	case "menu_support":
		b.deleteMessage(chatID, messageID)
		b.handleSupport(user, &tgbotapi.Message{Chat: query.Message.Chat})

	case "menu_piggy":
		b.deleteMessage(chatID, messageID)
		b.handlePiggy(user, &tgbotapi.Message{Chat: query.Message.Chat, Text: ""})

	case "add":
		b.deleteMessage(chatID, messageID)
		if parts[1] == "expense" {
			b.handleAddExpense(user, &tgbotapi.Message{Chat: query.Message.Chat})
		} else {
			b.handleAddIncome(user, &tgbotapi.Message{Chat: query.Message.Chat})
		}

	case "report":
		b.deleteMessage(chatID, messageID)
		b.handleReport(user, &tgbotapi.Message{Chat: query.Message.Chat}, parts[1])

	case "currency":
		b.DB.UpdateUserCurrency(user.ChatID, parts[1])
		b.DB.SetUserState(chatID, "onboarding_budget", "")
		b.editMessageText(chatID, messageID,
			fmt.Sprintf("Valjuta: %s\n\nTeper ustanovite mesjachnyj budzhet (vvedite chislo):", parts[1]),
			nil)

	case "cat":
		state, _ := b.DB.GetUserState(chatID)
		if state.State == "add_expense_category" {
			amount, _ := strconv.ParseFloat(state.Data, 64)
			category := parts[1]
			var familyID *int
			if user.FamilyID != nil {
				familyID = user.FamilyID
			}
			b.DB.AddTransaction(user.ID, familyID, "expense", amount, category, "")
			b.DB.ClearUserState(chatID)

			emoji := EmojiForCategory(category)
			text := fmt.Sprintf("Sohraneno!\n\n%s %s\n%s", emoji, category, FormatCurrency(amount, user.Currency))

			if alert, msgAlert := CheckBudgetAlert(b.DB, user); alert {
				text += "\n\n" + msgAlert
			}

			b.sendMessageWithKeyboard(chatID, text, mainMenuKeyboard(user.Plan))
		}

	case "upgrade":
		b.deleteMessage(chatID, messageID)
		plan := parts[1]
		b.sendMessage(chatID,
			fmt.Sprintf("Dlja oplaty ispolzujte:\n/pay %s\n\nDengi idut naprjamuju cherez Telegram Stars.", plan))

	case "piggy_create":
		b.deleteMessage(chatID, messageID)
		b.DB.SetUserState(chatID, "piggy_name", "")
		b.sendMessage(chatID, "Novaja kopilka\n\nVvedite nazvanie:")

	case "piggy_list":
		b.deleteMessage(chatID, messageID)
		b.handlePiggy(user, &tgbotapi.Message{Chat: query.Message.Chat, Text: ""})

	case "piggy_view":
		bankID, _ := strconv.Atoi(parts[1])
		bank, err := b.DB.GetPiggyBank(bankID)
		if err != nil {
			b.sendMessage(chatID, "Kopilka ne najdena")
			return
		}
		text := FormatPiggyBank(*bank, user.Currency)
		b.editMessageText(chatID, messageID, text, GetPiggyKeyboard(bankID))

	case "piggy_add":
		bankID, _ := strconv.Atoi(parts[1])
		b.DB.SetUserState(chatID, fmt.Sprintf("piggy_add_amount|%d", bankID), "")
		b.sendMessage(chatID, "Vvedite summu dlja popolnenija:")

	case "piggy_take":
		bankID, _ := strconv.Atoi(parts[1])
		bank, _ := b.DB.GetPiggyBank(bankID)
		if bank.IsLocked && bank.CurrentAmount < bank.TargetAmount {
			b.sendMessage(chatID, "Kopilka zamorozhena do dostizhenija celi!")
			return
		}
		b.DB.SetUserState(chatID, fmt.Sprintf("piggy_take_amount|%d", bankID), "")
		b.sendMessage(chatID, "Vvedite summu dlja snjatija:")

	case "piggy_del":
		bankID, _ := strconv.Atoi(parts[1])
		b.DB.DeletePiggyBank(bankID)
		b.sendMessageWithKeyboard(chatID, "Kopilka udalena.", mainMenuKeyboard(user.Plan))

	case "piggy_rule":
		bankID, _ := strconv.Atoi(parts[1])
		b.editMessageText(chatID, messageID, "Vyberite pravilo avtopopolnenija:", autoRuleKeyboard(bankID))

	case "rule":
		ruleType := parts[1]
		bankID, _ := strconv.Atoi(parts[2])
		if ruleType == "none" {
			b.DB.Exec("UPDATE piggy_banks SET auto_rule = '' WHERE id = ?", bankID)
			b.sendMessage(chatID, "Avtopopolnenie otkljucheno.")
			return
		}
		b.DB.SetUserState(chatID, fmt.Sprintf("piggy_rule_setup|%s|%d", ruleType, bankID), "")
		switch ruleType {
		case "percent":
			b.sendMessage(chatID, "Vvedite procent ot dohoda (naprimer: 10):")
		case "roundup":
			b.sendMessage(chatID, "Vvedite okruglenie (10, 50 ili 100):")
		case "penalty":
			b.sendMessage(chatID, "Vvedite kategoriju shtrafa (naprimer: Restorany):")
		case "fixed":
			b.sendMessage(chatID, "Vvedite summu i period cherez probel (naprimer: 500 daily):")
		}

	default:
		b.API.Send(tgbotapi.NewCallback(query.ID, ""))
	}

	b.API.Send(tgbotapi.NewCallback(query.ID, ""))
}

func (b *Bot) handleFSM(user *User, msg *tgbotapi.Message, state *UserState) {
	chatID := msg.Chat.ID
	text := msg.Text

	switch state.State {
	case "onboarding_currency":
		b.DB.ClearUserState(chatID)

	case "onboarding_budget":
		amount, err := strconv.ParseFloat(text, 64)
		if err != nil {
			b.sendMessage(chatID, "Vvedite chislo. Primer: 50000")
			return
		}
		b.DB.UpdateUserBudget(chatID, amount)
		b.DB.ClearUserState(chatID)
		b.sendMessageWithKeyboard(chatID,
			fmt.Sprintf("Budzhet: %s\n\nNastrojka zavershena!\n\nDobavte pervyj rashod: 500 produkty\nIli ispolzujte /menu", FormatCurrency(amount, user.Currency)),
			mainMenuKeyboard(user.Plan))

	case "set_budget":
		amount, err := strconv.ParseFloat(text, 64)
		if err != nil {
			b.sendMessage(chatID, "Vvedite chislo")
			return
		}
		b.DB.UpdateUserBudget(chatID, amount)
		b.DB.ClearUserState(chatID)
		b.sendMessageWithKeyboard(chatID, fmt.Sprintf("Budzhet obnovlen: %s", FormatCurrency(amount, user.Currency)), mainMenuKeyboard(user.Plan))

	case "add_expense_amount":
		amount, err := strconv.ParseFloat(text, 64)
		if err != nil {
			b.sendMessage(chatID, "Vvedite summu chislom")
			return
		}
		b.DB.SetUserState(chatID, "add_expense_category", fmt.Sprintf("%.2f", amount))
		b.sendMessageWithKeyboard(chatID, "Vyberite kategoriju:", categoryKeyboard("cat"))

	case "add_income_amount":
		amount, err := strconv.ParseFloat(text, 64)
		if err != nil {
			b.sendMessage(chatID, "Vvedite summu chislom")
			return
		}
		var familyID *int
		if user.FamilyID != nil {
			familyID = user.FamilyID
		}
		b.DB.AddTransaction(user.ID, familyID, "income", amount, "Dohod", "")
		b.DB.ClearUserState(chatID)
		b.sendMessageWithKeyboard(chatID, fmt.Sprintf("Dohod sohranen: %s", FormatCurrency(amount, user.Currency)), mainMenuKeyboard(user.Plan))

	case "create_family":
		if text == "" {
			b.sendMessage(chatID, "Vvedite nazvanie")
			return
		}
		family, err := b.DB.CreateFamily(text, user.ID)
		if err != nil {
			b.sendMessage(chatID, "Oshibka")
			return
		}
		b.DB.ClearUserState(chatID)
		b.sendMessageWithKeyboard(chatID, fmt.Sprintf("Semja %s sozdana!", family.Name), mainMenuKeyboard(user.Plan))

	case "invite_member":
		username := strings.TrimPrefix(text, "@")
		b.DB.ClearUserState(chatID)
		b.sendMessage(chatID, fmt.Sprintf("Priglashenie dlja @%s otpravleno! (V demo-rezhime)", username))

	case "goal_name":
		if text == "" {
			b.sendMessage(chatID, "Vvedite nazvanie celi")
			return
		}
		b.DB.SetUserState(chatID, "goal_amount", text)
		b.sendMessage(chatID, "Vvedite celevuju summu:")

	case "goal_amount":
		amount, err := strconv.ParseFloat(text, 64)
		if err != nil {
			b.sendMessage(chatID, "Vvedite chislo")
			return
		}
		name := state.Data
		var familyID *int
		if user.FamilyID != nil {
			familyID = user.FamilyID
		}
		b.DB.CreateGoal(user.ID, familyID, name, amount, "")
		b.DB.ClearUserState(chatID)
		monthly := amount / 12
		b.sendMessageWithKeyboard(chatID,
			fmt.Sprintf("Cel %s sozdana!\n%s\nOtkladyvajte %s/mes",
				name, FormatCurrency(amount, user.Currency), FormatCurrency(monthly, user.Currency)),
			mainMenuKeyboard(user.Plan))

	case "piggy_name":
		if text == "" {
			b.sendMessage(chatID, "Vvedite nazvanie")
			return
		}
		b.DB.SetUserState(chatID, "piggy_target", text)
		b.sendMessage(chatID, "Vvedite celevuju summu (0 — bez celi):")

	case "piggy_target":
		target, _ := strconv.ParseFloat(text, 64)
		name := state.Data
		var familyID *int
		if user.FamilyID != nil {
			familyID = user.FamilyID
		}
		id, err := b.DB.CreatePiggyBank(user.ID, familyID, name, target, "🐷", "#FF6B6B", "", false, "")
		if err != nil {
			b.sendMessage(chatID, "Oshibka")
			return
		}
		b.DB.ClearUserState(chatID)
		b.sendMessageWithKeyboard(chatID,
			fmt.Sprintf("Kopilka %s sozdana!\n\nUpravljajte: /piggy", name),
			GetPiggyKeyboard(id))

	default:
		if strings.HasPrefix(state.State, "piggy_add_amount|") {
			parts := strings.Split(state.State, "|")
			bankID, _ := strconv.Atoi(parts[1])
			amount, err := strconv.ParseFloat(text, 64)
			if err != nil || amount <= 0 {
				b.sendMessage(chatID, "Vvedite polozhitelnoe chislo")
				return
			}
			b.DB.UpdatePiggyBankAmount(bankID, amount)
			b.DB.ClearUserState(chatID)
			bank, _ := b.DB.GetPiggyBank(bankID)
			b.sendMessageWithKeyboard(chatID,
				fmt.Sprintf("Kopilka popolnena na %s!\n\n%s", FormatCurrency(amount, user.Currency), FormatPiggyBank(*bank, user.Currency)),
				GetPiggyKeyboard(bankID))
		} else if strings.HasPrefix(state.State, "piggy_take_amount|") {
			parts := strings.Split(state.State, "|")
			bankID, _ := strconv.Atoi(parts[1])
			amount, err := strconv.ParseFloat(text, 64)
			if err != nil || amount <= 0 {
				b.sendMessage(chatID, "Vvedite polozhitelnoe chislo")
				return
			}
			bank, _ := b.DB.GetPiggyBank(bankID)
			if bank.CurrentAmount < amount {
				b.sendMessage(chatID, "V kopilke nedostatochno sredstv")
				return
			}
			b.DB.UpdatePiggyBankAmount(bankID, -amount)
			b.DB.ClearUserState(chatID)
			bank, _ = b.DB.GetPiggyBank(bankID)
			b.sendMessageWithKeyboard(chatID,
				fmt.Sprintf("Snjato %s iz kopilki.\n\n%s", FormatCurrency(amount, user.Currency), FormatPiggyBank(*bank, user.Currency)),
				GetPiggyKeyboard(bankID))
		} else if strings.HasPrefix(state.State, "piggy_rule_setup|") {
			parts := strings.Split(state.State, "|")
			ruleType := parts[1]
			bankID, _ := strconv.Atoi(parts[2])

			var rule AutoRule
			rule.Type = ruleType

			switch ruleType {
			case "percent":
				p, _ := strconv.ParseFloat(text, 64)
				rule.Percent = p
			case "roundup":
				a, _ := strconv.ParseFloat(text, 64)
				rule.Amount = a
			case "penalty":
				rule.Category = text
				rule.Amount = 100
			case "fixed":
				fs := strings.Fields(text)
				if len(fs) >= 2 {
					a, _ := strconv.ParseFloat(fs[0], 64)
					rule.Amount = a
					rule.Period = fs[1]
				}
			}

			ruleJSON, _ := json.Marshal(rule)
			b.DB.Exec("UPDATE piggy_banks SET auto_rule = ? WHERE id = ?", string(ruleJSON), bankID)
			b.DB.ClearUserState(chatID)
			b.sendMessage(chatID, "Pravilo avtopopolnenija nastroeno!")
		}
	}
}

func (b *Bot) handleReferralStart(user *User, msg *tgbotapi.Message) {
	code := msg.CommandArguments()
	if code != "" && code != user.ReferralCode {
		var referrer User
		err := b.DB.Get(&referrer, "SELECT * FROM users WHERE referral_code = ?", code)
		if err == nil && referrer.ID != user.ID && user.ReferrerID == nil {
			b.DB.SetReferrer(user.ID, referrer.ID)
			b.sendMessage(msg.Chat.ID,
				fmt.Sprintf("Vy prisoedinilis po priglasheniju %s!\n\nDobro pozhalovat v Kopejku!", referrer.FirstName))
			b.sendMessage(referrer.ChatID,
				fmt.Sprintf("%s prisoedinilsja po vashej ssylke!", user.FirstName))
		}
	}
	b.handleStart(user, msg)
}

func planName(plan string) string {
	switch plan {
	case "premium":
		return "Premium"
	case "business":
		return "Business"
	default:
		return "Free"
	}
}

func periodName(p string) string {
	switch p {
	case "today":
		return "segodnja"
	case "week":
		return "nedelju"
	case "month":
		return "mesjac"
	default:
		return p
	}
}

func familyLimit(plan string) int {
	if plan == "business" {
		return 20
	}
	return 5
}

func generateChartURL(stats map[string]float64) string {
	labels := []string{}
	data := []float64{}
	for k, v := range stats {
		labels = append(labels, k)
		data = append(data, v)
	}
	chartData := map[string]interface{}{
		"type": "doughnut",
		"data": map[string]interface{}{
			"labels": labels,
			"datasets": []map[string]interface{}{
				{"data": data},
			},
		},
		"options": map[string]interface{}{
			"plugins": map[string]interface{}{
				"legend": map[string]bool{"display": true},
			},
		},
	}
	jsonData, _ := json.Marshal(chartData)
	return "https://quickchart.io/chart?c=" + string(jsonData)
}
