package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "os"
    "os/signal"
    "strconv"
    "strings"
    "sync"
    "syscall"
    "time"
    "unicode/utf8"
)

type Config struct {
    TelegramToken  string
    DeepSeekAPIKey string
    AdzunaAppID    string
    AdzunaAppKey   string
}

func loadConfig() *Config {
    return &Config{
        TelegramToken:  os.Getenv("TELEGRAM_BOT_TOKEN"),
        DeepSeekAPIKey: os.Getenv("DEEPSEEK_API_KEY"),
        AdzunaAppID:    os.Getenv("ADZUNA_APP_ID"),
        AdzunaAppKey:   os.Getenv("ADZUNA_APP_KEY"),
    }
}

type Job struct {
    Title       string
    Company     string
    Location    string
    Description string
    URL         string
    Salary      string
    HasTest     bool
    Source      string
}

type UserSession struct {
    State        string
    CVText       string
    Profession   string
    Country      string
    CurrentJobs  []Job
    JobIndex     int
    ChatID       int64
    LastActivity time.Time
}

var userSessions = make(map[int64]*UserSession)
var sessionMutex sync.RWMutex

func callDeepSeek(apiKey, prompt string) (string, error) {
    reqBody := map[string]interface{}{
        "model":       "deepseek-chat",
        "temperature": 0.7,
        "max_tokens":  500,
        "messages": []map[string]string{
            {"role": "user", "content": prompt},
        },
    }
    jsonData, _ := json.Marshal(reqBody)
    req, _ := http.NewRequest("POST", "https://api.deepseek.com/v1/chat/completions", bytes.NewBuffer(jsonData))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+apiKey)
    client := &http.Client{Timeout: 30 * time.Second}
    resp, err := client.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()
    body, _ := io.ReadAll(resp.Body)
    var result struct {
        Choices []struct {
            Message struct{ Content string }`json:"message"`
        }`json:"choices"`
    }
    json.Unmarshal(body, &result)
    if len(result.Choices) > 0 {
        return result.Choices[0].Message.Content, nil
    }
    return "", fmt.Errorf("нет ответа")
}

const MaxFileSize = 5 * 1024 * 1024

func validateFile(data []byte) error {
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

func detectProfession(cvText string) string {
    cvLower := strings.ToLower(cvText)
    keywords := map[string][]string{
        "Golang Developer":     {"golang", "go ", "goroutine", "go developer"},
        "Python Developer":     {"python", "django", "flask", "fastapi"},
        "Java Developer":       {"java", "spring", "hibernate", "maven"},
        "JavaScript Developer": {"javascript", "node.js", "react", "vue", "angular", "typescript"},
        "DevOps Engineer":      {"devops", "kubernetes", "docker", "terraform", "aws", "ci/cd"},
        "QA Engineer":          {"qa", "testing", "selenium", "тестировщик", "test case", "баг-трекер"},
        "Business/System Analyst": {
            "бизнес-аналитик", "business analyst", "системный аналитик", "system analyst",
            "use case", "user story", "bpmn", "uml", "rest", "soap", "kafka",
            "sql", "postgresql", "oracle", "cassandra", "confluence", "jira",
            "swagger", "postman", "аналитик", "сбор требований", "анализ требований",
            "техническое задание", "моделирование бизнес-процессов", "системная интеграция",
        },
        "Product Manager": {"product manager", "product owner", "agile", "scrum", "roadmap"},
        "Data Scientist":  {"data science", "machine learning", "tensorflow", "pytorch"},
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

type JobSearcher struct {
    adzunaAppID  string
    adzunaAppKey string
}

func newJobSearcher(cfg *Config) *JobSearcher {
    return &JobSearcher{
        adzunaAppID:  cfg.AdzunaAppID,
        adzunaAppKey: cfg.AdzunaAppKey,
    }
}

func (s *JobSearcher) searchAll(profession, country string) []Job {
    var allJobs []Job

    himalayasJobs := s.searchHimalayas(profession)
    allJobs = append(allJobs, himalayasJobs...)

    if s.adzunaAppID != "" && s.adzunaAppKey != "" {
        adzunaJobs := s.searchAdzuna(profession, country)
        allJobs = append(allJobs, adzunaJobs...)
    }

    if len(allJobs) == 0 {
        return s.getMockJobs(profession, country)
    }
    return allJobs
}

func (s *JobSearcher) searchHimalayas(profession string) []Job {
    encodedProfession := strings.ReplaceAll(profession, " ", "%20")
    apiURL := fmt.Sprintf("https://himalayas.app/jobs/api?search=%s&limit=10", encodedProfession)
    resp, err := http.Get(apiURL)
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
        }`json:"jobs"`
    }
    json.NewDecoder(resp.Body).Decode(&result)
    var jobs []Job
    for _, j := range result.Jobs {
        salary := ""
        if j.SalaryMin > 0 && j.SalaryMax > 0 {
            salary = fmt.Sprintf("%d - %d %s", j.SalaryMin, j.SalaryMax, j.Currency)
        }
        desc := j.Description
        if len(desc) > 200 {
            desc = desc[:200] + "..."
        }
        jobs = append(jobs, Job{
            Title:       j.Title,
            Company:     j.CompanyName,
            Location:    j.Location,
            Description: desc,
            URL:         j.URL,
            Salary:      salary,
            HasTest:     strings.Contains(strings.ToLower(j.Description), "test"),
            Source:      "Himalayas",
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
    case "france", "франция": countryCode = "fr"
    case "australia", "австралия": countryCode = "au"
    case "singapore", "сингапур": countryCode = "sg"
    }
    encodedProfession := strings.ReplaceAll(profession, " ", "%20")
    apiURL := fmt.Sprintf("https://api.adzuna.com/v1/api/jobs/%s/search/1?app_id=%s&app_key=%s&what=%s&results_per_page=10",
        countryCode, s.adzunaAppID, s.adzunaAppKey, encodedProfession)
    resp, err := http.Get(apiURL)
    if err != nil {
        return nil
    }
    defer resp.Body.Close()
    var result struct {
        Results []struct {
            Title       string `json:"title"`
            Company     struct{ DisplayName string }`json:"company"`
            Location    struct{ DisplayName string }`json:"location"`
            Description string `json:"description"`
            RedirectURL string `json:"redirect_url"`
            SalaryMin   float64 `json:"salary_min"`
            SalaryMax   float64 `json:"salary_max"`
        }`json:"results"`
    }
    json.NewDecoder(resp.Body).Decode(&result)
    var jobs []Job
    for _, j := range result.Results {
        salary := ""
        if j.SalaryMin > 0 && j.SalaryMax > 0 {
            salary = fmt.Sprintf("%.0f - %.0f", j.SalaryMin, j.SalaryMax)
        }
        desc := j.Description
        if len(desc) > 200 {
            desc = desc[:200] + "..."
        }
        jobs = append(jobs, Job{
            Title:       j.Title,
            Company:     j.Company.DisplayName,
            Location:    j.Location.DisplayName,
            Description: desc,
            URL:         j.RedirectURL,
            Salary:      salary,
            HasTest:     strings.Contains(strings.ToLower(j.Description), "test"),
            Source:      "Adzuna",
        })
    }
    return jobs
}

func (s *JobSearcher) getMockJobs(profession, country string) []Job {
    return []Job{
        {Title: fmt.Sprintf("Senior %s", profession), Company: "Tech Corp", Location: country, Description: "Разработка систем.", URL: "#", HasTest: true, Source: "Mock", Salary: "$80,000 - $100,000"},
        {Title: profession, Company: "Startup Inc", Location: country, Description: "Работа в agile-команде.", URL: "#", HasTest: false, Source: "Mock", Salary: "$60,000 - $80,000"},
    }
}

func generateCoverLetter(apiKey string, cvText, jobTitle, company string) string {
    if apiKey == "" {
        return fallbackLetter(cvText, jobTitle, company)
    }
    if len(cvText) > 500 {
        cvText = cvText[:500]
    }
    prompt := fmt.Sprintf("Напиши короткое сопроводительное письмо для отклика на вакансию %s в %s. Резюме: %s",
        jobTitle, company, cvText)
    resp, err := callDeepSeek(apiKey, prompt)
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

func professionKeyboard(detected string) map[string]interface{} {
    buttons := [][]map[string]interface{}{}
    if detected != "" {
        buttons = append(buttons, []map[string]interface{}{{"text": "✅ " + detected}})
    }
    buttons = append(buttons, [][]map[string]interface{}{
        {{"text": "Golang Developer"}, {"text": "Python Developer"}},
        {{"text": "QA Engineer"}, {"text": "Business/System Analyst"}},
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
            {{"text": "🔍 Новый поиск"}, {"text": "💬 Обратная связь"}},
            {{"text": "🆘 Помощь"}},
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
        },
    }
}

func removeKeyboard() map[string]interface{} {
    return map[string]interface{}{"remove_keyboard": true}
}

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
            Result struct{ FilePath string }`json:"result"`
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

    log.Printf("🚀 Job Hunter Bot запущен")

    go func() {
        http.Get(fmt.Sprintf("%s%s/deleteWebhook?drop_pending_updates=true", telegramAPI, cfg.TelegramToken))
        var offset int64 = 0
        ticker := time.NewTicker(2 * time.Second)
        defer ticker.Stop()
        for range ticker.C {
            updates := getUpdates(cfg.TelegramToken, offset)
            for _, upd := range updates {
                offset = upd.UpdateID + 1
                processUpdate(cfg, upd)
            }
        }
    }()

    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("OK"))
    })

    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }

    stop := make(chan os.Signal, 1)
    signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
    go func() { <-stop; os.Exit(0) }()

    log.Printf("🌐 HTTP-сервер запущен на порту %s", port)
    log.Fatal(http.ListenAndServe(":"+port, nil))
}

// ========== UPDATE ==========

type Update struct {
    UpdateID int64 `json:"update_id"`
    Message  *struct {
        MessageID int64 `json:"message_id"`
        Chat      struct{ ID int64 }`json:"chat"`
        Text      string `json:"text"`
        Document  *struct {
            FileID   string `json:"file_id"`
            FileName string `json:"file_name"`
        }`json:"document"`
        From struct {
            ID        int64  `json:"id"`
            Username  string `json:"username"`
            FirstName string `json:"first_name"`
        }`json:"from"`
    }`json:"message"`
    CallbackQuery *struct {
        ID      string `json:"id"`
        Data    string `json:"data"`
        Message *struct {
            Chat      struct{ ID int64 }`json:"chat"`
            MessageID int64 `json:"message_id"`
        }`json:"message"`
    }`json:"callback_query"`
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

// ========== PROCESS UPDATE ==========

func safeSubstring(s string, maxLen int) string {
    if len(s) <= maxLen {
        return s
    }
    return s[:maxLen]
}

func processUpdate(cfg *Config, upd Update) {
    token := cfg.TelegramToken
    jobSearcher := newJobSearcher(cfg)

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
            letter := generateCoverLetter(cfg.DeepSeekAPIKey, session.CVText, job.Title, job.Company)
            sendMessage(token, chatID, fmt.Sprintf("📧 Письмо для %s:\n\n%s\n\n🔗 %s\n\n⚠️ Отправьте отклик самостоятельно по ссылке.", job.Title, letter, job.URL), nil)
            session.JobIndex++
            showCurrentJob(token, session)
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
            sessionMutex.Lock()
            userSessions[chatID] = &UserSession{State: "awaiting_cv", ChatID: chatID, LastActivity: time.Now()}
            sessionMutex.Unlock()
            sendMessage(token, chatID, fmt.Sprintf("👋 Привет, @%s!\n\nОтправь PDF с резюме для начала.", username), removeKeyboard())

        case "/reset":
            sessionMutex.Lock()
            delete(userSessions, chatID)
            sessionMutex.Unlock()
            sendMessage(token, chatID, "🔄 Сброшено. Отправь PDF.", removeKeyboard())

        case "/help":
            sendMessage(token, chatID, "/start - начать\n/reset - сброс\n/how - как работает\n/feedback - связь с @Trene4ca", mainKeyboard())

        case "/how":
            sendMessage(token, chatID, "📋 Загрузите PDF, выберите страну, получите вакансии с кнопками «Откликнуться».\n\n⚠️ Бот генерирует письмо, а отклик отправляете вы сами по ссылке.", mainKeyboard())

        case "/feedback":
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
        if err := validateFile(fileData); err != nil {
            sendMessage(token, chatID, fmt.Sprintf("❌ %s", err.Error()), nil)
            return
        }
        cvText := extractTextFromPDF(fileData)
        if len(cvText) < 20 {
            sendMessage(token, chatID, "⚠️ Не удалось прочитать PDF.", nil)
            return
        }
        profession := detectProfession(cvText)
        log.Printf("Определена профессия: %s", profession)

        sessionMutex.Lock()
        userSessions[chatID] = &UserSession{
            State:        "awaiting_profession",
            CVText:       cvText,
            Profession:   profession,
            ChatID:       chatID,
            LastActivity: time.Now(),
        }
        sessionMutex.Unlock()

        sendMessage(token, chatID, fmt.Sprintf("✅ Определена профессия: *%s*\n\nВыберите или введите свою:", profession), professionKeyboard(profession))
        return
    }

    if msg.Text != "" {
        sessionMutex.RLock()
        session := userSessions[chatID]
        sessionMutex.RUnlock()

        if session == nil {
            sendMessage(token, chatID, "Нажмите /start", nil)
            return
        }

        switch msg.Text {
        case "🔍 Новый поиск":
            sessionMutex.Lock()
            session.State = "awaiting_cv"
            sessionMutex.Unlock()
            sendMessage(token, chatID, "Отправьте PDF с резюме", removeKeyboard())
            return
        }

        switch session.State {
        case "awaiting_profession":
            profession := strings.TrimPrefix(msg.Text, "✅ ")
            sessionMutex.Lock()
            session.Profession = profession
            session.State = "awaiting_country"
            sessionMutex.Unlock()
            sendMessage(token, chatID, "🌍 Выберите страну:", countryKeyboard())

        case "awaiting_country":
            country := strings.TrimPrefix(msg.Text, "🇳🇱 ")
            country = strings.TrimPrefix(country, "🇩🇪 ")
            country = strings.TrimPrefix(country, "🇬🇧 ")
            country = strings.TrimPrefix(country, "🇺🇸 ")
            country = strings.TrimPrefix(country, "🇨🇦 ")
            country = strings.TrimPrefix(country, "🇫🇷 ")

            sessionMutex.Lock()
            session.Country = country
            session.State = "browsing"
            profession := session.Profession
            sessionMutex.Unlock()

            sendMessage(token, chatID, fmt.Sprintf("🔍 Ищу *%s* в *%s*...", profession, country), nil)

            jobs := jobSearcher.searchAll(profession, country)
            log.Printf("Найдено %d вакансий для %s в %s", len(jobs), profession, country)

            if len(jobs) == 0 {
                sendMessage(token, chatID, "❌ Вакансий не найдено.", mainKeyboard())
                return
            }

            sessionMutex.Lock()
            session.CurrentJobs = jobs
            session.JobIndex = 0
            sessionMutex.Unlock()

            sendMessage(token, chatID, fmt.Sprintf("✅ Найдено *%d* вакансий:", len(jobs)), nil)
            showCurrentJob(token, session)

        default:
            sendMessage(token, chatID, "Используйте меню", mainKeyboard())
        }
    }
}

func showCurrentJob(token string, session *UserSession) {
    if session.JobIndex >= len(session.CurrentJobs) {
        sendMessage(token, session.ChatID, "🏁 Все вакансии просмотрены!", mainKeyboard())
        return
    }
    job := session.CurrentJobs[session.JobIndex]

    text := fmt.Sprintf(
        "*%d/%d*  %s\n🏢 %s\n📍 %s\n💰 %s\n📝 %s\n🔗 %s",
        session.JobIndex+1,
        len(session.CurrentJobs),
        job.Title,
        job.Company,
        job.Location,
        job.Salary,
        safeSubstring(job.Description, 250),
        job.URL,
    )

    log.Printf("Показываю вакансию %d: %s", session.JobIndex+1, job.Title)
    sendMessage(token, session.ChatID, text, jobActionKeyboard(session.JobIndex))
}
