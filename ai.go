package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type DeepSeekRequest struct {
	Model    string            `json:"model"`
	Messages []DeepSeekMessage `json:"messages"`
}

type DeepSeekMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type DeepSeekResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func GetAIAdvice(db *DB, userID int, apiKey string) (string, error) {
	stats, err := db.GetCategoryStats(userID, "month")
	if err != nil {
		return "", err
	}

	balance, err := db.GetBalance(userID)
	if err != nil {
		return "", err
	}

	forecast, avg, trend := ForecastExpenses(db, userID)

	prompt := fmt.Sprintf("Proanaliziruj moi finansy i daj 3-5 konkretnyh sovetov po ekonomii.\n\nBalans: %.2f\nRashody po kategorijam za mesjac:\n", balance)
	for cat, amount := range stats {
		prompt += fmt.Sprintf("- %s: %.2f\n", cat, amount)
	}
	prompt += fmt.Sprintf("\nPrognoz rashodov na konets mesjaca: %.2f\nSrednij rashod za 3 mesjaca: %.2f\nTrend: %s\n\nDavaj konkretnye rekomendacii s summami, kotorye mozhno sjekonomit. Otvetj kratko, po delu.", forecast, avg, trend)

	reqBody := DeepSeekRequest{
		Model: "deepseek-chat",
		Messages: []DeepSeekMessage{
			{Role: "system", Content: "Ty finansovyj assistent. Analiziruj rashody i davaj konkretnye sovety po ekonomii s realnymi ciframi. Budj lakonicnym."},
			{Role: "user", Content: prompt},
		},
	}

	jsonData, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "https://api.deepseek.com/chat/completions", bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result DeepSeekResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	if len(result.Choices) > 0 {
		return result.Choices[0].Message.Content, nil
	}
	return "Ne udalos poluchit sovet ot AI. Poprobujte pozhe.", nil
}
