package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "net/url"
    "os"
    "os/signal"
    "strconv"
    "strings"
    "sync"
    "syscall"
    "time"
    "unicode/utf8"

    "github.com/jmoiron/sqlx"
    _ "github.com/joho/godotenv/autoload"
    _ "github.com/mattn/go-sqlite3"
)

// ========== КОНФИГУРАЦИЯ ==========

type Config struct {
    TelegramToken   string
    AdminChatID     int64
    DeepSeekAPIKey  string
    SerpAPIKey      string
    AdzunaAppID     string
    AdzunaAppKey    string
    DatabasePath    string
}

func loadConfig() *Config {
    adminID, _ := strconv.ParseInt(os.Getenv("ADMIN_CHAT_ID"), 10, 64)
    dbPath := os.Getenv("DATABASE_PATH")
    if dbPath == "" {
        dbPath = "./bot.db"
    }
    return &Config{
        TelegramToken:  os.Getenv("TELEGRAM_BOT_TOKEN"),
        AdminChatID:    adminID,
        DeepSeekAPIKey: os.Getenv("DEEPSEEK_API_KEY"),
        SerpAPIKey:     os.Getenv("SERPAPI_KEY"),
        AdzunaAppID:    os.Getenv("ADZUNA_APP_ID"),
        AdzunaAppKey:   os.Getenv("ADZUNA_APP_KEY"),
        DatabasePath:   dbPath,
    }
}

// ========== МОДЕЛИ ==========

type User struct {
    ID           int64     `db:"id"`
    ChatID       int64     `db:"chat_id"`
    Username     string    `db:"username"`
    FirstName    string    `db:"first_name"`
    CVText       string    `db:"cv_text"`
    CVParsedData string    `db:"cv_parsed_data"`
    Profession   string    `db:"profession"`
    Country      string    `db:"country"`
    State        string    `db:"state"`
    CreatedAt    time.Time `db:"created_at"`
    UpdatedAt    time.Time `db:"updated_at"`
}

type Job struct {
    ID          string    `json:"id"`
    Title       string    `json:"title"`
    Company     string    `json:"company"`
    Location    string    `json:"location"`
    Description string    `json:"description"`
    URL         string    `json:"url"`
    Salary      string    `json:"salary"`
    HasTest     bool      `json:"has_test"`
    Source      string    `json:"source"`
    PostedAt    time.Time `json:"posted_at"`
    CreatedAt   time.Time `json:"created_at"`
}

type Stats struct {
    ID               int64 `db:"id"`
    TotalUsers       int   `db:"total_users"`
    TotalCVProcessed int   `db:"total_cv_processed"`
    TotalJobsViewed  int   `db:"total_jobs_viewed"`
    TotalApplied     int   `db:"total_applied"`
}

type UserSession struct {
    User         *User
    CurrentJobs  []Job
    JobIndex     int
    LastActivity time.Time
}

// ========== DEEPSEEK ЗАПРОСЫ (без внешних библиотек) ==========

type DeepSeekMessage struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

type DeepSeekRequest struct {
    Model       string            `json:"model"`
    Messages    []DeepSeekMessage `json:"messages"`
    Temperature float64           `json:"temperature"`
    MaxTokens   int               `json:"max_tokens"`
}

type DeepSeekResponse struct {
    Choices []struct {
        Message DeepSeekMessage `json:"message"`
    } `json:"choices"`
}

func callDeepSeek(apiKey string, prompt string) (string, error) {
    reqBody := DeepSeekRequest{
        Model:       "deepseek-chat",
        Temperature: 0.7,
        MaxTokens:   500,
        Messages: []DeepSeekMessage{
            {Role: "user", Content: prompt},
        },
    }

    jsonData, err := json.Marshal(reqBody)
    if err != nil {
        return "", err
    }

    req, err := http.NewRequest("POST", "https://api.deepseek.com/v1/chat/completions", bytes.NewBuffer(jsonData))
    if err != nil {
        return "", err
    }

    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+apiKey)

    client := &http.Client{Timeout: 30 * time.Second}
    resp, err := client.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return "", err
    }

    var result DeepSeekResponse
    if err := json.Unmarshal(body, &result); err != nil {
        return "", err
    }

    if len(result.Choices) > 0 {
        return result.Choices[0].Message.Content, nil
    }
    return "", fmt.Errorf("нет ответа от DeepSeek")
}

// ========== БАЗА ДАННЫХ ==========

var db *sqlx.DB
var userSessions = make(map[int64]*UserSession)
var sessionMutex sync.RWMutex

func initDB(dbPath string) {
    var err error
    db, err = sqlx.Open("sqlite3", dbPath)
    if err != nil {
        log.Fatal("Failed to open database:", err)
    }
    if err = db.Ping(); err != nil {
        log.Fatal("Failed to ping database:", err)
    }

    schema := `
    CREATE TABLE IF NOT EXISTS users (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        chat_id INTEGER UNIQUE NOT NULL,
        username TEXT,
        first_name TEXT,
        cv_text TEXT,
        cv_parsed_data TEXT,
        profession TEXT,
        country TEXT,
        state TEXT DEFAULT 'awaiting_cv',
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );
    CREATE TABLE IF NOT EXISTS saved_jobs (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        user_id INTEGER NOT NULL,
        job_id TEXT NOT NULL,
        job_data TEXT,
        applied BOOLEAN DEFAULT FALSE,
        saved_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );
    CREATE TABLE IF NOT EXISTS feedback (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        user_id INTEGER,
        username TEXT,
        message TEXT NOT NULL,
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );
    CREATE TABLE IF NOT EXISTS stats (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        total_users INTEGER DEFAULT 0,
        total_cv_processed INTEGER DEFAULT 0,
        total_jobs_viewed INTEGER DEFAULT 0,
        total_applied INTEGER DEFAULT 0
    );
    INSERT OR IGNORE INTO stats (id) VALUES (1);
    `
    db.MustExec(schema)
    log.Println("Database initialized")
}

func getUserByChatID(chatID int64) (*User, error) {
    var user User
    err := db.Get(&user, "SELECT * FROM users WHERE chat_id = ?", chatID)
    return &user, err
}

func createUser(user *User) error {
    now := time.Now()
    _, err := db.Exec(
        "INSERT INTO users (chat_id, username, first_name, state, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
        user.ChatID, user.Username, user.FirstName, user.State, now, now,
    )
    return err
}

func updateUser(user *User) error {
    user.UpdatedAt = time.Now()
    _, err := db.Exec(
        "UPDATE users SET username=?, first_name=?, cv_text=?, cv_parsed_data=?, profession=?, country=?, state=?, updated_at=? WHERE id=?",
        user.Username, user.FirstName, user.CVText, user.CVParsedData, user.Profession, user.Country, user.State, user.UpdatedAt, user.ID,
    )
    return err
}

func getOrCreateUser(chatID int64, username, firstName string) (*User, error) {
    user, err := getUserByChatID(chatID)
    if err == nil {
        return user, nil
    }
    user = &User{ChatID: chatID, Username: username, FirstName: firstName, State: "awaiting_cv"}
    err = createUser(user)
    if err == nil {
        db.Exec("UPDATE stats SET total_users = total_users + 1 WHERE id = 1")
    }
    return user, err
}

func deleteUserData(chatID int64) error {
    user, err := getUserByChatID(chatID)
    if err != nil {
        return err
    }
    db.Exec("DELETE FROM saved_jobs WHERE user_id = ?", user.ID)
    db.Exec("DELETE FROM feedback WHERE user_id = ?", user.ID)
    db.Exec("DELETE FROM users WHERE id = ?", user.ID)
    sessionMutex.Lock()
    delete(userSessions, chatID)
    sessionMutex.Unlock()
    return nil
}

func saveUserJob(userID int64, job *Job, applied bool) {
    jobData, _ := json.Marshal(job)
    db.Exec("INSERT INTO saved_jobs (user_id, job_id, job_data, applied) VALUES (?, ?, ?, ?)",
        userID, job.ID, string(jobData), applied)
    db.Exec("UPDATE stats SET total_jobs_viewed = total_jobs_viewed + 1 WHERE id = 1")
    if applied {
        db.Exec("UPDATE stats SET total_applied = total_applied + 1 WHERE id = 1")
    }
}

func incrementCVProcessed() {
    db.Exec("UPDATE stats SET total_cv_processed = total_cv_processed + 1 WHERE id = 1")
}

func saveFeedback(userID int64, username, message string) {
    db.Exec("INSERT INTO feedback (user_id, username, message) VALUES (?, ?, ?)", userID, username, message)
}

func getStats() Stats {
    var stats Stats
    db.Get(&stats, "SELECT * FROM stats WHERE id = 1")
    return stats
}

// ========== RATE LIMITER ==========

type RateLimiter struct {
    mu        sync.Mutex
    userCache map[int64][]time.Time
    limit     int
    window    time.Duration
}

func newRateLimiter(limit int, window time.Duration) *RateLimiter {
    return &RateLimiter{userCache: make(map[int64][]time.Time), limit: limit, window: window}
}

func (rl *RateLimiter) allow(userID int64) bool {
    rl.mu.Lock()
    defer rl.mu.Unlock()
    now := time.Now()
    cutoff := now.Add(-rl.window)
    var valid []time.Time
    for _, t := range rl.userCache[userID] {
        if t.After(cutoff) {
            valid = append(valid, t)
        }
    }
    if len(valid) >= rl.limit {
        return false
    }
    valid = append(valid, now)
    rl.userCache[userID] = valid
    return true
}

// ========== ВАЛИДАЦИЯ ФАЙЛОВ ==========

const MaxFileSize = 5 * 1024 * 1024

func validateFile(data []byte, fileName string) error {
    if len(data) > MaxFileSize {
        return fmt.Errorf("файл превышает 5 МБ")
    }
    return nil
}

func extractTextFromPDF(data []byte) string {
    var result strings.Builder
    for i := 0; i < len(data); i++ {
        if data[i] == '(' {
            start := i + 1
            end := start
            for end < len(data) && data[end] != ')' {
                end++
            }
            if end > start {
                chunk := string(data[start:end])
                if utf8.ValidString(chunk) && len(chunk) > 2 {
                    result.WriteString(chunk + " ")
                }
            }
            i = end
        }
    }
    text := result.String()
    if len(text) < 50 {
        return string(data)
    }
    return strings.Join(strings.Fields(text), " ")
}

// ========== ОПРЕДЕЛЕНИЕ ПРОФЕССИИ ==========

func detectProfession(cvText string) string {
    cvLower := strings.ToLower(cvText)
    keywords := map[string][]string{
        "Golang Developer": {"golang", "go ", "goroutine"},
        "Python Developer": {"python", "django", "flask", "fastapi"},
        "Java Developer":   {"java", "spring", "hibernate"},
        "JavaScript Developer": {"javascript", "node.js", "react", "vue", "angular"},
        "DevOps Engineer":  {"devops", "kubernetes", "docker", "terraform", "aws"},
        "QA Engineer":      {"qa", "testing", "selenium", "тестировщик"},
        "System Analyst":   {"system analyst", "системный аналитик", "use case", "uml", "api"},
        "Product Manager":  {"product manager", "product owner", "agile", "scrum"},
        "Data Scientist":   {"data science", "machine learning", "tensorflow", "pytorch"},
    }
    bestMatch := ""
    maxScore := 0
    for profession, words := range keywords {
        score := 0
        for _, word := range words {
            if strings.Contains(cvLower, word) {
                score += 10
            }
        }
        if score > maxScore {
            maxScore = score
            bestMatch = profession
        }
    }
    if bestMatch != "" {
        return bestMatch
    }
    return "Software Developer"
}

// ========== ПОИСК ВАКАНСИЙ ==========

type JobSearcher struct {
    serpAPIKey   string
    adzunaAppID  string
    adzunaAppKey string
}

func newJobSearcher(cfg *Config) *JobSearcher {
    return &JobSearcher{
        serpAPIKey:   cfg.SerpAPIKey,
        adzunaAppID:  cfg.AdzunaAppID,
        adzunaAppKey: cfg.AdzunaAppKey,
    }
}

func (s *JobSearcher) searchAll(profession, country string) []Job {
    var allJobs []Job

    allJobs = append(allJobs, s.searchHimalayas(profession)...)

    if s.serpAPIKey != "" {
        allJobs = append(allJobs, s.searchSerpAPI(profession, country)...)
    }

    if s.adzunaAppID != "" && s.adzunaAppKey != "" {
        allJobs = append(allJobs, s.searchAdzuna(profession, country)...)
    }

    if len(allJobs) == 0 {
        return s.getMockJobs(profession, country)
    }
    return allJobs
}

func (s *JobSearcher) searchHimalayas(profession string) []Job {
    apiURL := fmt.Sprintf("https://himalayas.app/jobs/api?search=%s&limit=10", url.QueryEscape(profession))
    client := &http.Client{Timeout: 10 * time.Second}
    resp, err := client.Get(apiURL)
    if err != nil {
        return nil
    }
    defer resp.Body.Close()
    var result struct {
        Jobs []struct {
            ID          string `json:"id"`
            Title       string `json:"title"`
            CompanyName string `json:"company_name"`
            Location    string `json:"location"`
            Description string `json:"description"`
            URL         string `json:"url"`
            SalaryMin   int    `json:"salary_min"`
            SalaryMax   int    `json:"salary_max"`
            Currency    string `json:"currency"`
        } `json:"jobs"`
    }
    json.NewDecoder(resp.Body).Decode(&result)
    var jobs []Job
    for _, j := range result.Jobs {
        salary := ""
        if j.SalaryMin > 0 && j.SalaryMax > 0 {
            salary = fmt.Sprintf("%d - %d %s", j.SalaryMin, j.SalaryMax, j.Currency)
        }
        jobs = append(jobs, Job{
            ID:          j.ID,
            Title:       j.Title,
            Company:     j.CompanyName,
            Location:    j.Location,
            Description: j.Description,
            URL:         j.URL,
            Salary:      salary,
            HasTest:     strings.Contains(strings.ToLower(j.Description), "test"),
            Source:      "Himalayas",
            PostedAt:    time.Now(),
            CreatedAt:   time.Now(),
        })
    }
    return jobs
}

func (s *JobSearcher) searchSerpAPI(profession, country string) []Job {
    query := url.QueryEscape(fmt.Sprintf("%s jobs in %s", profession, country))
    apiURL := fmt.Sprintf("https://serpapi.com/search?engine=google_jobs&q=%s&api_key=%s", query, s.serpAPIKey)
    resp, err := http.Get(apiURL)
    if err != nil {
        return nil
    }
    defer resp.Body.Close()
    var result struct {
        JobsResults []struct {
            Title       string `json:"title"`
            CompanyName string `json:"company_name"`
            Location    string `json:"location"`
            Description string `json:"description"`
            Link        string `json:"link"`
        } `json:"jobs_results"`
    }
    json.NewDecoder(resp.Body).Decode(&result)
    var jobs []Job
    for _, j := range result.JobsResults {
        jobs = append(jobs, Job{
            ID:          fmt.Sprintf("serp_%d", time.Now().UnixNano()),
            Title:       j.Title,
            Company:     j.CompanyName,
            Location:    j.Location,
            Description: j.Description,
            URL:         j.Link,
            HasTest:     strings.Contains(strings.ToLower(j.Description), "test"),
            Source:      "SerpAPI",
            PostedAt:    time.Now(),
            CreatedAt:   time.Now(),
        })
    }
    return jobs
}

func (s *JobSearcher) searchAdzuna(profession, country string) []Job {
    countryCode := "gb"
    switch strings.ToLower(country) {
    case "netherlands", "нидерланды": countryCode = "nl"
    case "germany", "германия": countryCode = "de"
    case "usa", "сша": countryCode = "us"
    case "canada", "канада": countryCode = "ca"
    }
    apiURL := fmt.Sprintf("https://api.adzuna.com/v1/api/jobs/%s/search/1?app_id=%s&app_key=%s&what=%s",
        countryCode, s.adzunaAppID, s.adzunaAppKey, url.QueryEscape(profession))
    resp, err := http.Get(apiURL)
    if err != nil {
        return nil
    }
    defer resp.Body.Close()
    var result struct {
        Results []struct {
            Title       string `json:"title"`
            Company     struct{ DisplayName string } `json:"company"`
            Location    struct{ DisplayName string } `json:"location"`
            Description string `json:"description"`
            RedirectURL string `json:"redirect_url"`
            SalaryMin   float64 `json:"salary_min"`
            SalaryMax   float64 `json:"salary_max"`
        } `json:"results"`
    }
    json.NewDecoder(resp.Body).Decode(&result)
    var jobs []Job
    for _, j := range result.Results {
        salary := ""
        if j.SalaryMin > 0 && j.SalaryMax > 0 {
            salary = fmt.Sprintf("%.0f - %.0f", j.SalaryMin, j.SalaryMax)
        }
        jobs = append(jobs, Job{
            ID:          fmt.Sprintf("adz_%d", time.Now().UnixNano()),
            Title:       j.Title,
            Company:     j.Company.DisplayName,
            Location:    j.Location.DisplayName,
            Description: j.Description,
            URL:         j.RedirectURL,
            Salary:      salary,
            HasTest:     strings.Contains(strings.ToLower(j.Description), "test"),
            Source:      "Adzuna",
            PostedAt:    time.Now(),
            CreatedAt:   time.Now(),
        })
    }
    return jobs
}

func (s *JobSearcher) getMockJobs(profession, country string) []Job {
    return []Job{
        {ID: "mock1", Title: profession, Company: "Demo Company", Location: country, Description: "Тестовая вакансия", URL: "#", HasTest: false, Source: "Mock", PostedAt: time.Now(), CreatedAt: time.Now()},
    }
}

// ========== AI-СЕРВИСЫ ==========

type AIService struct {
    deepseekKey string
    rateLimiter *RateLimiter
}

func newAIService(apiKey string, rateLimiter *RateLimiter) *AIService {
    return &AIService{deepseekKey: apiKey, rateLimiter: rateLimiter}
}

func (a *AIService) generateCoverLetter(userID int64, cvText, jobTitle, company, language string) string {
    if a.deepseekKey == "" {
        return fallbackLetter(cvText, jobTitle, company)
    }
    if !a.rateLimiter.allow(userID) {
        return "⏳ Слишком много запросов. Подождите минуту."
    }
    prompt := fmt.Sprintf("Напиши короткое сопроводительное письмо для отклика на вакансию %s в %s на %s языке. Резюме: %s",
        jobTitle, company, language, cvText[:min(500, len(cvText))])
    resp, err := callDeepSeek(a.deepseekKey, prompt)
    if err != nil {
        return fallbackLetter(cvText, jobTitle, company)
    }
    return resp
}

func fallbackLetter(cvText, jobTitle, company string) string {
    name := "Candidate"
    if words := strings.Fields(cvText); len(words) > 0 && len(words[0]) < 30 {
        name = words[0]
    }
    return fmt.Sprintf("Dear Hiring Manager,\n\nI am applying for %s at %s.\n\nBest regards,\n%s", jobTitle, company, name)
}

func min(a, b int) int { if a < b { return a }; return b }

// ========== КЛАВИАТУРЫ ==========

func professionKeyboard(detected string) map[string]interface{} {
    buttons := [][]map[string]interface{}{}
    if detected != "" {
        buttons = append(buttons, []map[string]interface{}{{"text": "✅ " + detected}})
    }
    buttons = append(buttons, [][]map[string]interface{}{
        {{"text": "Golang Developer"}, {"text": "Python Developer"}},
        {{"text": "QA Engineer"}, {"text": "System Analyst"}},
        {{"text": "Product Manager"}, {"text": "DevOps Engineer"}},
    }...)
    return map[string]interface{}{"keyboard": buttons, "resize_keyboard": true}
}

func countryKeyboard() map[string]interface{} {
    return map[string]interface{}{
        "keyboard": [][]map[string]interface{}{
            {{"text": "🇳🇱 Netherlands"}, {"text": "🇩🇪 Germany"}},
            {{"text": "🇬🇧 UK"}, {"text": "🇺🇸 USA"}},
            {{"text": "🇨🇦 Canada"}, {"text": "🇫🇷 France"}},
            {{"text": "🌍 Remote"}},
        },
        "resize_keyboard": true,
    }
}

func mainKeyboard() map[string]interface{} {
    return map[string]interface{}{
        "keyboard": [][]map[string]interface{}{
            {{"text": "🔍 Новый поиск"}, {"text": "📊 Статистика"}},
            {{"text": "💬 Обратная связь"}, {"text": "🆘 Помощь"}},
        },
        "resize_keyboard": true,
    }
}

func jobActionKeyboard(index int) map[string]interface{} {
    return map[string]interface{}{
        "inline_keyboard": [][]map[string]interface{}{
            {
                {"text": "✅ Откликнуться", "callback_data": fmt.Sprintf("apply_%d", index)},
                {"text": "⏩ Пропустить", "callback_data": fmt.Sprintf("skip_%d", index)},
            },
            {
                {"text": "💾 Сохранить", "callback_data": fmt.Sprintf("save_%d", index)},
                {"text": "🔔 Подписаться", "callback_data": fmt.Sprintf("subscribe_%d", index)},
            },
        },
    }
}

func removeKeyboard() map[string]interface{} {
    return map[string]interface{}{"remove_keyboard": true}
}

// ========== TELEGRAM API ==========

const telegramAPI = "https://api.telegram.org/bot"

func sendMessage(token string, chatID int64, text string, keyboard interface{}) {
    url := fmt.Sprintf("%s%s/sendMessage", telegramAPI, token)
    payload := map[string]interface{}{"chat_id": chatID, "text": text, "parse_mode": "Markdown"}
    if keyboard != nil {
        payload["reply_markup"] = keyboard
    }
    data, _ := json.Marshal(payload)
    http.Post(url, "application/json", bytes.NewReader(data))
}

func answerCallback(token, callbackID, text string) {
    url := fmt.Sprintf("%s%s/answerCallbackQuery", telegramAPI, token)
    payload := map[string]interface{}{"callback_query_id": callbackID, "text": text}
    data, _ := json.Marshal(payload)
    http.Post(url, "application/json", bytes.NewReader(data))
}

func getFile(token, fileID string) string {
    url := fmt.Sprintf("%s%s/getFile?file_id=%s", telegramAPI, token, fileID)
    resp, _ := http.Get(url)
    if resp != nil {
        defer resp.Body.Close()
        var result struct {
            OK     bool `json:"ok"`
            Result struct{ FilePath string } `json:"result"`
        }
        json.NewDecoder(resp.Body).Decode(&result)
        return result.Result.FilePath
    }
    return ""
}

func downloadFile(token, filePath string) ([]byte, error) {
    url := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", token, filePath)
    resp, err := http.Get(url)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    return io.ReadAll(resp.Body)
}

// ========== MAIN ==========

func main() {
    cfg := loadConfig()
    if cfg.TelegramToken == "" {
        log.Fatal("TELEGRAM_BOT_TOKEN is required")
    }

    initDB(cfg.DatabasePath)
    defer db.Close()

    rateLimiter := newRateLimiter(5, time.Minute)
    jobSearcher := newJobSearcher(cfg)
    aiService := newAIService(cfg.DeepSeekAPIKey, rateLimiter)

    log.Printf("🚀 Job Hunter Bot запущен")

    http.Get(fmt.Sprintf("%s%s/deleteWebhook?drop_pending_updates=true", telegramAPI, cfg.TelegramToken))

    var offset int64 = 0
    ticker := time.NewTicker(2 * time.Second)
    defer ticker.Stop()

    stop := make(chan os.Signal, 1)
    signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

    go func() {
        for range ticker.C {
            updates := getUpdates(cfg.TelegramToken, offset)
            for _, upd := range updates {
                offset = upd.UpdateID + 1
                processUpdate(cfg, jobSearcher, aiService, upd)
            }
        }
    }()

    <-stop
    log.Println("Бот остановлен")
}

type Update struct {
    UpdateID int64 `json:"update_id"`
    Message  *struct {
        MessageID int64 `json:"message_id"`
        Chat      struct{ ID int64 } `json:"chat"`
        Text      string `json:"text"`
        Document  *struct {
            FileID   string `json:"file_id"`
            FileName string `json:"file_name"`
        } `json:"document"`
        From struct {
            ID        int64  `json:"id"`
            Username  string `json:"username"`
            FirstName string `json:"first_name"`
        } `json:"from"`
    } `json:"message"`
    CallbackQuery *struct {
        ID      string `json:"id"`
        Data    string `json:"data"`
        Message *struct {
            Chat      struct{ ID int64 } `json:"chat"`
            MessageID int64              `json:"message_id"`
        } `json:"message"`
    } `json:"callback_query"`
}

func getUpdates(token string, offset int64) []Update {
    url := fmt.Sprintf("%s%s/getUpdates?timeout=1&offset=%d", telegramAPI, token, offset)
    resp, _ := http.Get(url)
    if resp == nil {
        return nil
    }
    defer resp.Body.Close()
    var result struct {
        OK     bool     `json:"ok"`
        Result []Update `json:"result"`
    }
    json.NewDecoder(resp.Body).Decode(&result)
    return result.Result
}

func processUpdate(cfg *Config, jobSearcher *JobSearcher, aiService *AIService, upd Update) {
    token := cfg.TelegramToken

    if upd.CallbackQuery != nil {
        cq := upd.CallbackQuery
        chatID := cq.Message.Chat.ID
        data := cq.Data

        sessionMutex.RLock()
        session := userSessions[chatID]
        sessionMutex.RUnlock()

        if session == nil {
            answerCallback(token, cq.ID, "Сессия устарела. /start")
            return
        }

        parts := strings.SplitN(data, "_", 2)
        if len(parts) != 2 {
            return
        }
        action, idxStr := parts[0], parts[1]
        idx, _ := strconv.Atoi(idxStr)

        if idx >= len(session.CurrentJobs) {
            answerCallback(token, cq.ID, "Вакансия недоступна")
            return
        }
        job := session.CurrentJobs[idx]

        switch action {
        case "skip":
            answerCallback(token, cq.ID, "⏩ Пропущено")
            session.JobIndex++
            showCurrentJob(token, session)

        case "apply":
            answerCallback(token, cq.ID, "📧 Генерирую письмо...")
            letter := aiService.generateCoverLetter(chatID, session.User.CVText, job.Title, job.Company, "en")
            sendMessage(token, chatID, fmt.Sprintf("📧 *Письмо:*\n\n%s", letter), nil)
            saveUserJob(session.User.ID, &job, true)
            session.JobIndex++
            showCurrentJob(token, session)

        case "save":
            answerCallback(token, cq.ID, "💾 Сохранено")
            saveUserJob(session.User.ID, &job, false)
            sendMessage(token, chatID, "💾 Вакансия сохранена", nil)

        case "subscribe":
            answerCallback(token, cq.ID, "🔔 Подписка оформлена")
            session.User.CVParsedData = fmt.Sprintf(`{"profession":"%s","country":"%s","subscribed":true}`, session.User.Profession, session.User.Country)
            updateUser(session.User)
            sendMessage(token, chatID, "🔔 Вы подписаны на ежедневную рассылку!", nil)
        }
        return
    }

    if upd.Message == nil {
        return
    }
    msg := upd.Message
    chatID := msg.Chat.ID
    username := msg.From.Username
    firstName := msg.From.FirstName
    if username == "" {
        username = firstName
    }

    if strings.HasPrefix(msg.Text, "/") {
        switch msg.Text {
        case "/start":
            user, _ := getOrCreateUser(chatID, username, firstName)
            sessionMutex.Lock()
            userSessions[chatID] = &UserSession{User: user, LastActivity: time.Now()}
            sessionMutex.Unlock()
            sendMessage(token, chatID, fmt.Sprintf("👋 Привет, @%s!\n\nОтправь PDF с резюме для начала.", username), removeKeyboard())

        case "/reset":
            sessionMutex.Lock()
            delete(userSessions, chatID)
            sessionMutex.Unlock()
            sendMessage(token, chatID, "🔄 Сброшено. Отправь PDF.", removeKeyboard())

        case "/stats":
            stats := getStats()
            msg := fmt.Sprintf("📊 *Статистика:*\n👥 Пользователей: %d\n📄 CV: %d\n👀 Просмотров: %d\n📤 Откликов: %d",
                stats.TotalUsers, stats.TotalCVProcessed, stats.TotalJobsViewed, stats.TotalApplied)
            sendMessage(token, chatID, msg, mainKeyboard())

        case "/help":
            sendMessage(token, chatID, "🆘 /start - начать\n/reset - сброс\n/stats - статистика\n/forget_me - удалить данные\n/feedback - обратная связь", mainKeyboard())

        case "/forget_me":
            deleteUserData(chatID)
            sendMessage(token, chatID, "✅ Все ваши данные удалены. /start для начала.", removeKeyboard())

        case "/feedback":
            user, _ := getUserByChatID(chatID)
            if user != nil {
                user.State = "awaiting_feedback"
                updateUser(user)
            }
            sendMessage(token, chatID, "💬 Напишите сообщение для @Trene4ca:", removeKeyboard())

        default:
            sendMessage(token, chatID, "Неизвестная команда. /help", nil)
        }
        return
    }

    if msg.Document != nil {
        filePath := getFile(token, msg.Document.FileID)
        fileData, err := downloadFile(token, filePath)
        if err != nil {
            sendMessage(token, chatID, "❌ Ошибка загрузки", nil)
            return
        }
        if err := validateFile(fileData, msg.Document.FileName); err != nil {
            sendMessage(token, chatID, fmt.Sprintf("❌ %s", err.Error()), nil)
            return
        }
        cvText := extractTextFromPDF(fileData)
        if len(cvText) < 20 {
            sendMessage(token, chatID, "⚠️ Не удалось прочитать PDF", nil)
            return
        }
        profession := detectProfession(cvText)
        incrementCVProcessed()

        user, _ := getOrCreateUser(chatID, username, firstName)
        user.CVText = cvText
        user.Profession = profession
        user.State = "awaiting_profession"
        updateUser(user)

        sessionMutex.Lock()
        userSessions[chatID] = &UserSession{User: user, LastActivity: time.Now()}
        sessionMutex.Unlock()

        if cfg.AdminChatID != 0 {
            sendMessage(token, cfg.AdminChatID, fmt.Sprintf("📄 @%s загрузил резюме. Профессия: %s", username, profession), nil)
        }

        sendMessage(token, chatID, fmt.Sprintf("✅ Определена профессия: *%s*\nВыберите или введите свою:", profession), professionKeyboard(profession))
        return
    }

    if msg.Text != "" {
        user, _ := getUserByChatID(chatID)
        if user == nil {
            sendMessage(token, chatID, "Нажмите /start", nil)
            return
        }

        switch msg.Text {
        case "🔍 Новый поиск":
            user.State = "awaiting_cv"
            updateUser(user)
            sendMessage(token, chatID, "Отправьте PDF с резюме", removeKeyboard())
            return
        case "📊 Статистика":
            stats := getStats()
            sendMessage(token, chatID, fmt.Sprintf("📊 Пользователей: %d | CV: %d | Откликов: %d", stats.TotalUsers, stats.TotalCVProcessed, stats.TotalApplied), mainKeyboard())
            return
        case "💬 Обратная связь":
            user.State = "awaiting_feedback"
            updateUser(user)
            sendMessage(token, chatID, "💬 Напишите сообщение для @Trene4ca:", removeKeyboard())
            return
        case "🆘 Помощь":
            sendMessage(token, chatID, "/start - начать\n/reset - сброс\n/forget_me - удалить данные", mainKeyboard())
            return
        }

        switch user.State {
        case "awaiting_feedback":
            saveFeedback(user.ID, username, msg.Text)
            if cfg.AdminChatID != 0 {
                sendMessage(token, cfg.AdminChatID, fmt.Sprintf("💬 Обратная связь от @%s:\n%s", username, msg.Text), nil)
            }
            user.State = "browsing"
            updateUser(user)
            sendMessage(token, chatID, "✅ Спасибо! Сообщение отправлено.", mainKeyboard())

        case "awaiting_profession":
            user.Profession = strings.TrimPrefix(msg.Text, "✅ ")
            user.State = "awaiting_country"
            updateUser(user)
            sendMessage(token, chatID, "🌍 Выберите страну:", countryKeyboard())

        case "awaiting_country":
            country := strings.TrimPrefix(msg.Text, "🇳🇱 ")
            country = strings.TrimPrefix(country, "🇩🇪 ")
            country = strings.TrimPrefix(country, "🇬🇧 ")
            country = strings.TrimPrefix(country, "🇺🇸 ")
            country = strings.TrimPrefix(country, "🇨🇦 ")
            country = strings.TrimPrefix(country, "🇫🇷 ")
            user.Country = country
            user.State = "browsing"
            updateUser(user)

            sendMessage(token, chatID, fmt.Sprintf("🔍 Ищу %s в %s...", user.Profession, user.Country), nil)

            jobs := jobSearcher.searchAll(user.Profession, user.Country)

            sessionMutex.Lock()
            session := userSessions[chatID]
            if session == nil {
                session = &UserSession{User: user}
                userSessions[chatID] = session
            }
            session.CurrentJobs = jobs
            session.JobIndex = 0
            sessionMutex.Unlock()

            if len(jobs) == 0 {
                sendMessage(token, chatID, "❌ Вакансий не найдено", mainKeyboard())
                return
            }

            sendMessage(token, chatID, fmt.Sprintf("✅ Найдено %d вакансий", len(jobs)), removeKeyboard())
            showCurrentJob(token, session)

        default:
            sendMessage(token, chatID, "Используйте меню", mainKeyboard())
        }
    }
}

func showCurrentJob(token string, session *UserSession) {
    if session.JobIndex >= len(session.CurrentJobs) {
        sendMessage(token, session.User.ChatID, "🏁 Всё просмотрено!", mainKeyboard())
        return
    }
    job := session.CurrentJobs[session.JobIndex]
    text := fmt.Sprintf("*%d/%d*  %s\n🏢 %s\n📍 %s\n💰 %s\n📝 %s",
        session.JobIndex+1, len(session.CurrentJobs), job.Title, job.Company, job.Location, job.Salary, job.Description[:min(200, len(job.Description))]+"...")
    sendMessage(token, session.User.ChatID, text, jobActionKeyboard(session.JobIndex))
}