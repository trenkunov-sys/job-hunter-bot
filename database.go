package main

import (
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	*sqlx.DB
}

func NewDB(path string) (*DB, error) {
	db, err := sqlx.Connect("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	return &DB{db}, nil
}

func (db *DB) Migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		chat_id INTEGER UNIQUE NOT NULL,
		username TEXT,
		first_name TEXT,
		plan TEXT DEFAULT 'free',
		family_id INTEGER,
		currency TEXT DEFAULT 'RUB',
		monthly_budget REAL DEFAULT 0,
		referrer_id INTEGER,
		referral_code TEXT UNIQUE,
		referral_count INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS families (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		owner_id INTEGER NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS transactions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		family_id INTEGER,
		type TEXT NOT NULL,
		amount REAL NOT NULL,
		category TEXT,
		description TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS subscriptions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		plan TEXT DEFAULT 'free',
		start_date DATE,
		end_date DATE,
		status TEXT DEFAULT 'active'
	);

	CREATE TABLE IF NOT EXISTS categories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		emoji TEXT,
		is_default BOOLEAN DEFAULT TRUE
	);

	CREATE TABLE IF NOT EXISTS user_categories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		family_id INTEGER,
		name TEXT NOT NULL,
		emoji TEXT
	);

	CREATE TABLE IF NOT EXISTS goals (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		family_id INTEGER,
		name TEXT NOT NULL,
		target_amount REAL NOT NULL,
		current_amount REAL DEFAULT 0,
		deadline DATE,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS payments (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		amount REAL NOT NULL,
		currency TEXT DEFAULT 'RUB',
		payment_method TEXT,
		status TEXT DEFAULT 'pending'
	);

	CREATE TABLE IF NOT EXISTS user_states (
		user_id INTEGER PRIMARY KEY,
		state TEXT DEFAULT '',
		data TEXT DEFAULT '',
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS piggy_banks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		family_id INTEGER,
		name TEXT NOT NULL,
		target_amount REAL DEFAULT 0,
		current_amount REAL DEFAULT 0,
		emoji TEXT DEFAULT '🐷',
		color TEXT DEFAULT '#FF6B6B',
		deadline DATE,
		is_locked BOOLEAN DEFAULT FALSE,
		auto_rule TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_transactions_user ON transactions(user_id);
	CREATE INDEX IF NOT EXISTS idx_transactions_date ON transactions(created_at);
	CREATE INDEX IF NOT EXISTS idx_transactions_family ON transactions(family_id);
	CREATE INDEX IF NOT EXISTS idx_piggy_user ON piggy_banks(user_id);
	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}

	defaults := []struct{ name, emoji string }{
		{"Продукты", "🛒"}, {"Транспорт", "🚇"}, {"Жильё", "🏠"},
		{"Коммуналка", "💡"}, {"Связь", "📱"}, {"Здоровье", "💊"},
		{"Развлечения", "🎬"}, {"Рестораны", "🍽"}, {"Одежда", "👕"},
		{"Образование", "📚"}, {"Подарки", "🎁"}, {"Путешествия", "✈️"},
		{"Автомобиль", "🚗"}, {"Дети", "👶"}, {"Питомцы", "🐾"},
		{"Налоги", "📄"}, {"Сбережения", "💰"}, {"Инвестиции", "📈"},
		{"Другое", "📦"},
	}
	for _, d := range defaults {
		db.Exec("INSERT OR IGNORE INTO categories (name, emoji, is_default) VALUES (?, ?, TRUE)", d.name, d.emoji)
	}
	return nil
}
func (db *DB) GetOrCreateUser(chatID int64, username, firstName string) (*User, error) {
	var user User
	err := db.Get(&user, "SELECT * FROM users WHERE chat_id = ?", chatID)
	if err == nil {
		return &user, nil
	}

	code := fmt.Sprintf("REF%d", chatID)
	res, err := db.Exec(
		"INSERT INTO users (chat_id, username, first_name, referral_code) VALUES (?, ?, ?, ?)",
		chatID, username, firstName, code,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	user.ID = int(id)
	user.ChatID = chatID
	user.Username = username
	user.FirstName = firstName
	user.Plan = "free"
	user.Currency = "RUB"
	user.ReferralCode = code
	return &user, nil
}

func (db *DB) GetUserByChatID(chatID int64) (*User, error) {
	var user User
	err := db.Get(&user, "SELECT * FROM users WHERE chat_id = ?", chatID)
	return &user, err
}

func (db *DB) GetUserByID(id int) (*User, error) {
	var user User
	err := db.Get(&user, "SELECT * FROM users WHERE id = ?", id)
	return &user, err
}

func (db *DB) UpdateUserPlan(userID int, plan string) error {
	_, err := db.Exec("UPDATE users SET plan = ? WHERE id = ?", plan, userID)
	return err
}

func (db *DB) UpdateUserCurrency(chatID int64, currency string) error {
	_, err := db.Exec("UPDATE users SET currency = ? WHERE chat_id = ?", currency, chatID)
	return err
}

func (db *DB) UpdateUserBudget(chatID int64, budget float64) error {
	_, err := db.Exec("UPDATE users SET monthly_budget = ? WHERE chat_id = ?", budget, chatID)
	return err
}

func (db *DB) SetReferrer(userID int, referrerID int) error {
	_, err := db.Exec("UPDATE users SET referrer_id = ? WHERE id = ? AND referrer_id IS NULL", referrerID, userID)
	if err == nil {
		db.Exec("UPDATE users SET referral_count = referral_count + 1 WHERE id = ?", referrerID)
	}
	return err
}

func (db *DB) AddTransaction(userID int, familyID *int, tType string, amount float64, category, description string) error {
	if familyID != nil {
		_, err := db.Exec(
			"INSERT INTO transactions (user_id, family_id, type, amount, category, description) VALUES (?, ?, ?, ?, ?, ?)",
			userID, *familyID, tType, amount, category, description)
		return err
	}
	_, err := db.Exec(
		"INSERT INTO transactions (user_id, type, amount, category, description) VALUES (?, ?, ?, ?, ?)",
		userID, tType, amount, category, description)
	return err
}

func (db *DB) GetTransactions(userID int, limit int) ([]Transaction, error) {
	var txs []Transaction
	err := db.Select(&txs, "SELECT * FROM transactions WHERE user_id = ? ORDER BY created_at DESC LIMIT ?", userID, limit)
	return txs, err
}

func (db *DB) GetTransactionsByPeriod(userID int, period string) ([]Transaction, error) {
	var query string
	switch period {
	case "today":
		query = "SELECT * FROM transactions WHERE user_id = ? AND date(created_at) = date('now') ORDER BY created_at DESC"
	case "week":
		query = "SELECT * FROM transactions WHERE user_id = ? AND created_at >= date('now', '-7 days') ORDER BY created_at DESC"
	case "month":
		query = "SELECT * FROM transactions WHERE user_id = ? AND created_at >= date('now', '-30 days') ORDER BY created_at DESC"
	default:
		query = "SELECT * FROM transactions WHERE user_id = ? ORDER BY created_at DESC LIMIT 50"
	}
	var txs []Transaction
	err := db.Select(&txs, query, userID)
	return txs, err
}

func (db *DB) GetBalance(userID int) (float64, error) {
	var balance float64
	err := db.Get(&balance, "SELECT COALESCE(SUM(CASE WHEN type = 'income' THEN amount ELSE -amount END), 0) FROM transactions WHERE user_id = ?", userID)
	return balance, err
}

func (db *DB) GetCategoryStats(userID int, period string) (map[string]float64, error) {
	query := `SELECT category, SUM(amount) as total FROM transactions WHERE user_id = ? AND type = 'expense'`
	switch period {
	case "today":
		query += " AND date(created_at) = date('now')"
	case "week":
		query += " AND created_at >= date('now', '-7 days')"
	case "month":
		query += " AND created_at >= date('now', '-30 days')"
	}
	query += " GROUP BY category"

	rows, err := db.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make(map[string]float64)
	for rows.Next() {
		var cat string
		var total float64
		rows.Scan(&cat, &total)
		stats[cat] = total
	}
	return stats, nil
}

func (db *DB) GetMonthlyExpense(userID int, yearMonth string) (float64, error) {
	var total float64
	err := db.Get(&total,
		"SELECT COALESCE(SUM(amount), 0) FROM transactions WHERE user_id = ? AND type = 'expense' AND strftime('%Y-%m', created_at) = ?",
		userID, yearMonth)
	return total, err
}

func (db *DB) GetCategories() ([]Category, error) {
	var cats []Category
	err := db.Select(&cats, "SELECT * FROM categories WHERE is_default = TRUE ORDER BY name")
	return cats, err
}

func (db *DB) CreateFamily(name string, ownerID int) (*Family, error) {
	res, err := db.Exec("INSERT INTO families (name, owner_id) VALUES (?, ?)", name, ownerID)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	db.Exec("UPDATE users SET family_id = ? WHERE id = ?", int(id), ownerID)
	return &Family{ID: int(id), Name: name, OwnerID: ownerID}, nil
}

func (db *DB) GetFamily(familyID int) (*Family, error) {
	var f Family
	err := db.Get(&f, "SELECT * FROM families WHERE id = ?", familyID)
	return &f, err
}

func (db *DB) GetFamilyMembers(familyID int) ([]User, error) {
	var users []User
	err := db.Select(&users, "SELECT * FROM users WHERE family_id = ?", familyID)
	return users, err
}

func (db *DB) AddToFamily(userID, familyID int) error {
	_, err := db.Exec("UPDATE users SET family_id = ? WHERE id = ?", familyID, userID)
	return err
}

func (db *DB) CreateGoal(userID int, familyID *int, name string, target float64, deadline string) error {
	if deadline != "" {
		if familyID != nil {
			_, err := db.Exec("INSERT INTO goals (user_id, family_id, name, target_amount, deadline) VALUES (?, ?, ?, ?, ?)",
				userID, *familyID, name, target, deadline)
			return err
		}
		_, err := db.Exec("INSERT INTO goals (user_id, name, target_amount, deadline) VALUES (?, ?, ?, ?)",
			userID, name, target, deadline)
		return err
	}
	if familyID != nil {
		_, err := db.Exec("INSERT INTO goals (user_id, family_id, name, target_amount) VALUES (?, ?, ?, ?)",
			userID, *familyID, name, target)
		return err
	}
	_, err := db.Exec("INSERT INTO goals (user_id, name, target_amount) VALUES (?, ?, ?)",
		userID, name, target)
	return err
}

func (db *DB) GetGoals(userID int) ([]Goal, error) {
	var goals []Goal
	err := db.Select(&goals,
		"SELECT * FROM goals WHERE user_id = ? OR family_id IN (SELECT family_id FROM users WHERE id = ?)",
		userID, userID)
	return goals, err
}

func (db *DB) UpdateGoalAmount(goalID int, amount float64) error {
	_, err := db.Exec("UPDATE goals SET current_amount = current_amount + ? WHERE id = ?", amount, goalID)
	return err
}

func (db *DB) CreatePayment(userID int, amount float64, currency, method string) (int, error) {
	res, err := db.Exec(
		"INSERT INTO payments (user_id, amount, currency, payment_method, status) VALUES (?, ?, ?, ?, 'pending')",
		userID, amount, currency, method)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return int(id), nil
}

func (db *DB) UpdatePaymentStatus(paymentID int, status string) error {
	_, err := db.Exec("UPDATE payments SET status = ? WHERE id = ?", status, paymentID)
	return err
}

func (db *DB) CountTransactions(userID int, month string) (int, error) {
	var count int
	err := db.Get(&count, "SELECT COUNT(*) FROM transactions WHERE user_id = ? AND strftime('%Y-%m', created_at) = ?", userID, month)
	return count, err
}

func (db *DB) GetAllTransactions(userID int) ([]Transaction, error) {
	var txs []Transaction
	err := db.Select(&txs, "SELECT * FROM transactions WHERE user_id = ? ORDER BY created_at DESC", userID)
	return txs, err
}

func (db *DB) SetUserState(userID int64, state, data string) error {
	_, err := db.Exec(
		"INSERT INTO user_states (user_id, state, data, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(user_id) DO UPDATE SET state=excluded.state, data=excluded.data, updated_at=excluded.updated_at",
		userID, state, data, time.Now())
	return err
}

func (db *DB) GetUserState(userID int64) (*UserState, error) {
	var s UserState
	err := db.Get(&s, "SELECT * FROM user_states WHERE user_id = ?", userID)
	if err != nil {
		return &UserState{UserID: userID, State: "", Data: ""}, nil
	}
	return &s, nil
}

func (db *DB) ClearUserState(userID int64) error {
	_, err := db.Exec("DELETE FROM user_states WHERE user_id = ?", userID)
	return err
}

func (db *DB) CreatePiggyBank(userID int, familyID *int, name string, target float64, emoji, color string, deadline string, isLocked bool, autoRule string) (int, error) {
	var res interface{}
	var err error
	if familyID != nil {
		res, err = db.Exec(
			"INSERT INTO piggy_banks (user_id, family_id, name, target_amount, emoji, color, deadline, is_locked, auto_rule) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			userID, *familyID, name, target, emoji, color, deadline, isLocked, autoRule)
	} else {
		res, err = db.Exec(
			"INSERT INTO piggy_banks (user_id, name, target_amount, emoji, color, deadline, is_locked, auto_rule) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			userID, name, target, emoji, color, deadline, isLocked, autoRule)
	}
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return int(id), nil
}

func (db *DB) GetPiggyBanks(userID int) ([]PiggyBank, error) {
	var banks []PiggyBank
	err := db.Select(&banks,
		"SELECT * FROM piggy_banks WHERE user_id = ? OR family_id IN (SELECT family_id FROM users WHERE id = ?) ORDER BY created_at DESC",
		userID, userID)
	return banks, err
}

func (db *DB) GetPiggyBank(id int) (*PiggyBank, error) {
	var bank PiggyBank
	err := db.Get(&bank, "SELECT * FROM piggy_banks WHERE id = ?", id)
	return &bank, err
}

func (db *DB) UpdatePiggyBankAmount(id int, delta float64) error {
	_, err := db.Exec("UPDATE piggy_banks SET current_amount = current_amount + ? WHERE id = ?", delta, id)
	return err
}

func (db *DB) DeletePiggyBank(id int) error {
	_, err := db.Exec("DELETE FROM piggy_banks WHERE id = ?", id)
	return err
}

func (db *DB) GetTotalPiggySavings(userID int) (float64, error) {
	var total float64
	err := db.Get(&total, "SELECT COALESCE(SUM(current_amount), 0) FROM piggy_banks WHERE user_id = ?", userID)
	return total, err
}
