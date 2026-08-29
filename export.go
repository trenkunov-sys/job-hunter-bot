package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func ExportToCSV(db *DB, userID int) (string, error) {
	txs, err := db.GetAllTransactions(userID)
	if err != nil {
		return "", err
	}

	filename := fmt.Sprintf("export_%d_%d.csv", userID, time.Now().Unix())
	path := filepath.Join("exports", filename)
	os.MkdirAll("exports", 0755)

	file, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	writer.Write([]string{"ID", "Type", "Amount", "Category", "Description", "Date"})
	for _, tx := range txs {
		writer.Write([]string{
			fmt.Sprintf("%d", tx.ID),
			tx.Type,
			fmt.Sprintf("%.2f", tx.Amount),
			tx.Category,
			tx.Description,
			tx.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return path, nil
}
