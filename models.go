package main

import "time"

type User struct {
	ID            int       `db:"id"`
	ChatID        int64     `db:"chat_id"`
	Username      string    `db:"username"`
	FirstName     string    `db:"first_name"`
	Plan          string    `db:"plan"`
	FamilyID      *int      `db:"family_id"`
	Currency      string    `db:"currency"`
	MonthlyBudget float64   `db:"monthly_budget"`
	ReferrerID    *int      `db:"referrer_id"`
	ReferralCode  string    `db:"referral_code"`
	ReferralCount int       `db:"referral_count"`
	CreatedAt     time.Time `db:"created_at"`
}

type Family struct {
	ID        int       `db:"id"`
	Name      string    `db:"name"`
	OwnerID   int       `db:"owner_id"`
	CreatedAt time.Time `db:"created_at"`
}

type Transaction struct {
	ID          int       `db:"id"`
	UserID      int       `db:"user_id"`
	FamilyID    *int      `db:"family_id"`
	Type        string    `db:"type"`
	Amount      float64   `db:"amount"`
	Category    string    `db:"category"`
	Description string    `db:"description"`
	CreatedAt   time.Time `db:"created_at"`
}

type Subscription struct {
	ID        int        `db:"id"`
	UserID    int        `db:"user_id"`
	Plan      string     `db:"plan"`
	StartDate *time.Time `db:"start_date"`
	EndDate   *time.Time `db:"end_date"`
	Status    string     `db:"status"`
}

type Category struct {
	ID        int    `db:"id"`
	Name      string `db:"name"`
	Emoji     string `db:"emoji"`
	IsDefault bool   `db:"is_default"`
}

type UserCategory struct {
	ID       int    `db:"id"`
	UserID   int    `db:"user_id"`
	FamilyID *int   `db:"family_id"`
	Name     string `db:"name"`
	Emoji    string `db:"emoji"`
}

type Goal struct {
	ID            int        `db:"id"`
	UserID        int        `db:"user_id"`
	FamilyID      *int       `db:"family_id"`
	Name          string     `db:"name"`
	TargetAmount  float64    `db:"target_amount"`
	CurrentAmount float64    `db:"current_amount"`
	Deadline      *time.Time `db:"deadline"`
	CreatedAt     time.Time  `db:"created_at"`
}

type Payment struct {
	ID            int     `db:"id"`
	UserID        int     `db:"user_id"`
	Amount        float64 `db:"amount"`
	Currency      string  `db:"currency"`
	PaymentMethod string  `db:"payment_method"`
	Status        string  `db:"status"`
}

type UserState struct {
	UserID    int64     `db:"user_id"`
	State     string    `db:"state"`
	Data      string    `db:"data"`
	UpdatedAt time.Time `db:"updated_at"`
}

type PiggyBank struct {
	ID            int        `db:"id"`
	UserID        int        `db:"user_id"`
	FamilyID      *int       `db:"family_id"`
	Name          string     `db:"name"`
	TargetAmount  float64    `db:"target_amount"`
	CurrentAmount float64    `db:"current_amount"`
	Emoji         string     `db:"emoji"`
	Color         string     `db:"color"`
	Deadline      *time.Time `db:"deadline"`
	IsLocked      bool       `db:"is_locked"`
	AutoRule      string     `db:"auto_rule"`
	CreatedAt     time.Time  `db:"created_at"`
}

type AutoRule struct {
	Type     string  `json:"type"`
	Amount   float64 `json:"amount"`
	Percent  float64 `json:"percent"`
	Category string  `json:"category"`
	Period   string  `json:"period"`
}
