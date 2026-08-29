package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type YooKassaPayment struct {
	Amount struct {
		Value    string `json:"value"`
		Currency string `json:"currency"`
	} `json:"amount"`
	Confirmation struct {
		Type      string `json:"type"`
		ReturnURL string `json:"return_url"`
	} `json:"confirmation"`
	Capture     bool   `json:"capture"`
	Description string `json:"description"`
}

type YooKassaResponse struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Confirmation struct {
		ConfirmationURL string `json:"confirmation_url"`
	} `json:"confirmation"`
}

func CreateYooKassaPayment(shopID, secretKey string, amount float64, currency, desc string) (string, string, error) {
	reqBody := YooKassaPayment{}
	reqBody.Amount.Value = fmt.Sprintf("%.2f", amount)
	reqBody.Amount.Currency = currency
	reqBody.Confirmation.Type = "redirect"
	reqBody.Confirmation.ReturnURL = "https://t.me/your_bot"
	reqBody.Capture = true
	reqBody.Description = desc

	jsonData, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "https://api.yookassa.ru/v3/payments", bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotence-Key", fmt.Sprintf("%d", time.Now().UnixNano()))
	req.SetBasicAuth(shopID, secretKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result YooKassaResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", err
	}

	return result.ID, result.Confirmation.ConfirmationURL, nil
}

func CreateStripePayment(secretKey string, amount float64, currency string) (string, string, error) {
	amountCents := int64(amount * 100)
	data := fmt.Sprintf("amount=%d&currency=%s&payment_method_types[0]=card", amountCents, currency)
	req, _ := http.NewRequest("POST", "https://api.stripe.com/v1/payment_intents", bytes.NewBufferString(data))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+secretKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(body, &result)

	if id, ok := result["id"].(string); ok {
		if clientSecret, ok := result["client_secret"].(string); ok {
			return id, clientSecret, nil
		}
	}
	return "", "", fmt.Errorf("failed to create stripe payment")
}
