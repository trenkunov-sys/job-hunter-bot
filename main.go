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
    if apiKey == "" {
        return "", fmt.Errorf("API ключ не задан")
    }
    reqBody := map[string]interface{}{
        "model":       "deepseek-chat",
        "temperature": 0.7,
        "max_tokens":  500,
        "messages": []map[string]string{{"role": "user", "content": prompt}},
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
    return "", fmt.Errorf("пустой ответ")
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

    // Приоритет: аналитик (твоё резюме)
    analystWords := []string{
        "бизнес-аналитик", "business analyst", "системный аналитик", "system analyst",
        "аналитик", "use case", "user story", "bpmn", "uml",
        "rest", "soap", "kafka", "sql", "postgresql", "oracle", "cassandra",
        "confluence", "jira", "swagger", "postman",
        "сбор требований", "анализ требований", "техническое задание",
        "моделирование бизнес-процессов", "системная интеграция",
        "спецификации", "декомпозиция", "валидация",
    }
    analystScore := 0
    for _, word := range analystWords {
        if strings.Contains(cvLower, word) {
            analystScore += 10
        }
    }
    if analystScore >= 30 {
        return "Business/System Analyst"
    }

    // Остальные профессии
    keywords := map[string][]string{
        "Golang Developer":     {"golang", "go ", "goroutine"},
        "Python Developer":     {"python", "django", "flask"},
        "Java Developer":       {"java", "spring", "hibernate"},
        "JavaScript Developer": {"javascript", "node.js", "react", "vue"},
        "DevOps Engineer":      {"devops", "kubernetes", "docker", "aws"},
        "QA Engineer":          {"qa ", "testing", "selenium", "тестировщик"},
        "Product Manager":      {"product manager", "agile", "scrum"},
        "Data Scientist":       {"data science", "machine learning", "tensorflow"},
    }
    bestMatch := "Software Developer"
    maxScore := 0
    for profession, words := range keywords {
        score := 0
        for _, word := range words {
            if strings.Contains(cvLower, word) {
                score += 10
            }
        }
        if score > maxScore && score > analystScore {
            maxScore = score
            bestMatch = profession
        }
    }
    if maxScore > analystScore {
        return bestMatch
    }
    return "Business/System Analyst"
}

func stripHTML(html string) string {
    var result strings.Builder
    inTag := false
    for _, r := range html {
        if r == '<' { inTag = true; continue }
        if r == '>' { inTag = false; result.WriteRune(' '); continue }
        if !inTag { result.WriteRune(r) }
    }
    return strings.Join(strings.Fields(result.String()), " ")
}

type JobSearcher struct {
    adzunaAppID  string
    adzunaAppKey string
}

func newJobSearcher(cfg *Config) *JobSearcher {
    return &JobSearcher{adzunaAppID: cfg.AdzunaAppID, adzunaAppKey: cfg.AdzunaAppKey}
}

func (s *JobSearcher) searchAll(profession, country string) []Job {
    var allJobs []Job
    searchTerm := s.mapProfessionToSearchTerm(profession)
    allJobs = append(allJobs, s.searchHimalayas(searchTerm)...)
    if s.adzunaAppID != "" && s.adzunaAppKey != "" {
        allJobs = append(allJobs, s.searchAdzuna(searchTerm, country)...)
    }
    if len(allJobs) == 0 {
        return s.getMockJobs(profession, country)
    }
    return allJobs
}

func (s *JobSearcher) mapProfessionToSearchTerm(profession string) string {
    p := strings.ToLower(profession)
    if strings.Contains(p, "analyst") || strings.Contains(p, "аналитик") { return "business analyst" }
    if strings.Contains(p, "golang") { return "golang developer" }
    if strings.Contains(p, "python") { return "python developer" }
    if strings.Contains(p, "qa") { return "qa engineer" }
    if strings.Contains(p, "product") { return "product manager" }
    return profession
}

func (s *JobSearcher) searchHimalayas(profession string) []Job {
    encodedProfession := strings.ReplaceAll(profession, " ", "%20")
    apiURL := fmt.Sprintf("https://himalayas.app/jobs/api?search=%s&limit=5", encodedProfession)
    resp, err := http.Get(apiURL)
    if err != nil { return nil }
    defer resp.Body.Close()
    var result struct {
        Jobs []struct {
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
        company := j.CompanyName
        if company == "" { company = "Himalayas" }
        desc := stripHTML(j.Description)
        if len(desc) > 300 { desc = desc[:300] + "..." }
        if desc == "" { desc = "Описание недоступно" }
        jobs = append(jobs, Job{
            Title: j.Title, Company: company, Location: j.Location,
            Description: desc, URL: j.URL, Salary: salary,
            HasTest: strings.Contains(strings.ToLower(j.Description), "test"),
            Source: "Himalayas",
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
    }
    encodedProfession := strings.ReplaceAll(profession, " ", "%20")
    apiURL := fmt.Sprintf("https://api.adzuna.com/v1/api/jobs/%s/search/1?app_id=%s&app_key=%s&what=%s&results_per_page=5",
        countryCode, s.adzunaAppID, s.adzunaAppKey, encodedProfession)
    resp, err := http.Get(apiURL)
    if err != nil { return nil }
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
        company := j.Company.DisplayName
        if company == "" { company = "Adzuna" }
        desc := stripHTML(j.Description)
        if len(desc) > 300 { desc = desc[:300] + "..." }
        if desc == "" { desc = "Описание недоступно" }
        jobs = append(jobs, Job{
            Title: j.Title, Company: company, Location: j.Location.DisplayName,
            Description: desc, URL: j.RedirectURL, Salary: salary,
            HasTest: strings.Contains(strings.ToLower(j.Description), "test"),
            Source: "Adzuna",
        })
    }
    return jobs
}

func (s *JobSearcher) getMockJobs(profession, country string) []Job {
    return []Job{
        {Title: fmt.Sprintf("Senior %s", profession), Company: "Tech Corp", Location: country, Description: "Разработка систем.", URL: "#", HasTest: false, Source: "Mock", Salary: "$80,000 - $100,000"},
        {Title: profession, Company: "Startup Inc", Location: country, Description: "Agile-команда.", URL: "#", HasTest: false, Source: "Mock", Salary: "$60,000 - $80,000"},
    }
}

func translateText(apiKey, text string) string {
    if apiKey == "" || len(text) < 20 { return text }
    resp, err := callDeepSeek(apiKey, fmt.Sprintf("Переведи на русский: %s", text))
    if err != nil { return text }
    return resp
}

func generateCoverLetter(apiKey, cvText, jobTitle, company string) string {
    if apiKey == "" { return fallbackLetter(cvText, jobTitle, company) }
    if len(cvText) > 500 { cvText = cvText[:500] }
    resp, err := callDeepSeek(apiKey, fmt.Sprintf("Напиши сопроводительное письмо на русском для отклика на вакансию %s в компанию %s. Резюме: %s", jobTitle, company, cvText))
    if err != nil { return fallbackLetter(cvText, jobTitle, company) }
    return resp
}

func fallbackLetter(cvText, jobTitle, company string) string {
    name := "Candidate"
    if words := strings.Fields(cvText); len(words) > 0 && len(words[0]) < 30 { name = words[0] }
    return fmt.Sprintf("Здравствуйте!\n\nЯ откликаюсь на вакансию %s в компанию %s.\n\nМой опыт соответствует требованиям.\n\nС уважением,\n%s", jobTitle, company, name)
}

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
        }, "resize_keyboard": true,
    }
}

func mainKeyboard() map[string]interface{} {
    return map[string]interface{}{
        "keyboard": [][]map[string]interface{}{
            {{"text": "🔍 Новый поиск"}, {"text": "💬 Обратная связь"}},
            {{"text": "🆘 Помощь"}},
        }, "resize_keyboard": true,
    }
}

func jobActionKeyboard(index int) map[string]interface{} {
    return map[string]interface{}{
        "inline_keyboard": [][]map[string]interface{}{
            {{"text": "✅ Откликнуться", "callback_data": fmt.Sprintf("apply_%d", index)}, {"text": "⏩ Пропустить", "callback_data": fmt.Sprintf("skip_%d", index)}},
        },
    }
}

func removeKeyboard() map[string]interface{} {
    return map[string]interface{}{"remove_keyboard": true}
}

const telegramAPI = "https://api.telegram.org/bot"

func sendMessage(token string, chatID int64, text string, keyboard interface{}) {
    url := fmt.Sprintf("%s%s/sendMessage", telegramAPI, token)
    payload := map[string]interface{}{"chat_id": chatID, "text": text, "parse_mode": "HTML", "disable_web_page_preview": false}
    if keyboard != nil { payload["reply_markup"] = keyboard }
    data, _ := json.Marshal(payload)
    http.Post(url, "application/json", bytes.NewReader(data))
}

func answerCallback(token, callbackID, text string) {
    url := fmt.Sprintf("%s%s/answerCallbackQuery", telegramAPI, token)
    data, _ := json.Marshal(map[string]interface{}{"callback_query_id": callbackID, "text": text})
    http.Post(url, "application/json", bytes.NewReader(data))
}

func getFile(token, fileID string) string {
    url := fmt.Sprintf("%s%s/getFile?file_id=%s", telegramAPI, token, fileID)
    resp, _ := http.Get(url)
    if resp != nil {
        defer resp.Body.Close()
        var r struct { OK bool `json:"ok"`; Result struct{ FilePath string }`json:"result"` }
        json.NewDecoder(resp.Body).Decode(&r)
        return r.Result.FilePath
    }
    return ""
}

func downloadFile(token, filePath string) ([]byte, error) {
    url := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", token, filePath)
    resp, err := http.Get(url)
    if err != nil { return nil, err }
    defer resp.Body.Close()
    return io.ReadAll(resp.Body)
}

// MAIN
func main() {
    cfg := loadConfig()
    if cfg.TelegramToken == "" { log.Fatal("TELEGRAM_BOT_TOKEN is required") }
    log.Printf("🚀 Job Hunter Bot запущен")
    go func() {
        http.Get(fmt.Sprintf("%s%s/deleteWebhook?drop_pending_updates=true", telegramAPI, cfg.TelegramToken))
        var offset int64 = 0
        ticker := time.NewTicker(2 * time.Second)
        defer ticker.Stop()
        for range ticker.C {
            for _, upd := range getUpdates(cfg.TelegramToken, offset) {
                offset = upd.UpdateID + 1
                processUpdate(cfg, upd)
            }
        }
    }()
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("OK")) })
    port := os.Getenv("PORT")
    if port == "" { port = "8080" }
    stop := make(chan os.Signal, 1)
    signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
    go func() { <-stop; os.Exit(0) }()
    log.Fatal(http.ListenAndServe(":"+port, nil))
}

// UPDATE
type Update struct {
    UpdateID int64 `json:"update_id"`
    Message  *struct {
        MessageID int64 `json:"message_id"`; Chat struct{ ID int64 }`json:"chat"`
        Text string `json:"text"`; Document *struct{ FileID string `json:"file_id"`; FileName string `json:"file_name"` }`json:"document"`
        From struct { ID int64 `json:"id"`; Username string `json:"username"`; FirstName string `json:"first_name"` }`json:"from"`
    }`json:"message"`
    CallbackQuery *struct {
        ID string `json:"id"`; Data string `json:"data"`
        Message *struct { Chat struct{ ID int64 }`json:"chat"`; MessageID int64 `json:"message_id"` }`json:"message"`
    }`json:"callback_query"`
}

func getUpdates(token string, offset int64) []Update {
    url := fmt.Sprintf("%s%s/getUpdates?timeout=1&offset=%d", telegramAPI, token, offset)
    resp, _ := http.Get(url)
    if resp == nil { return nil }
    defer resp.Body.Close()
    var r struct { OK bool `json:"ok"`; Result []Update `json:"result"` }
    json.NewDecoder(resp.Body).Decode(&r)
    return r.Result
}

func processUpdate(cfg *Config, upd Update) {
    token := cfg.TelegramToken
    js := newJobSearcher(cfg)

    if upd.CallbackQuery != nil {
        cq := upd.CallbackQuery
        chatID := cq.Message.Chat.ID
        sessionMutex.RLock()
        s := userSessions[chatID]
        sessionMutex.RUnlock()
        if s == nil { answerCallback(token, cq.ID, "Сессия устарела"); return }
        parts := strings.SplitN(cq.Data, "_", 2)
        if len(parts) != 2 { return }
        idx, _ := strconv.Atoi(parts[1])
        if idx >= len(s.CurrentJobs) { answerCallback(token, cq.ID, "Вакансия недоступна"); return }
        job := s.CurrentJobs[idx]
        switch parts[0] {
        case "skip":
            s.JobIndex++
            showCurrentJob(token, s, cfg.DeepSeekAPIKey)
        case "apply":
            letter := generateCoverLetter(cfg.DeepSeekAPIKey, s.CVText, job.Title, job.Company)
            msg := fmt.Sprintf("<b>📧 Письмо для %s в %s:</b>\n\n%s\n\n🔗 <a href=\"%s\">Открыть вакансию</a>\n\n⚠️ Отправьте отклик самостоятельно по ссылке.", job.Title, job.Company, letter, job.URL)
            sendMessage(token, chatID, msg, nil)
            s.JobIndex++
            showCurrentJob(token, s, cfg.DeepSeekAPIKey)
        }
        return
    }

    if upd.Message == nil { return }
    msg := upd.Message
    chatID := msg.Chat.ID
    username := msg.From.Username
    if username == "" { username = msg.From.FirstName }

    if strings.HasPrefix(msg.Text, "/") {
        switch msg.Text {
        case "/start":
            sessionMutex.Lock()
            userSessions[chatID] = &UserSession{State: "awaiting_cv", ChatID: chatID, LastActivity: time.Now()}
            sessionMutex.Unlock()
            sendMessage(token, chatID, fmt.Sprintf("👋 Привет, @%s!\n\nОтправь PDF с резюме.", username), removeKeyboard())
        case "/reset":
            sessionMutex.Lock(); delete(userSessions, chatID); sessionMutex.Unlock()
            sendMessage(token, chatID, "🔄 Сброшено.", removeKeyboard())
        case "/help":
            sendMessage(token, chatID, "/start /reset /how /feedback\nСвязь: @Trene4ca", mainKeyboard())
        case "/how":
            sendMessage(token, chatID, "📋 Загрузи PDF → выбери страну → получи вакансии → нажми «Откликнуться». Бот напишет письмо, отклик отправишь сам по ссылке.", mainKeyboard())
        case "/feedback":
            sendMessage(token, chatID, "💬 Напиши @Trene4ca:", removeKeyboard())
        }
        return
    }

    if msg.Document != nil {
        fp := getFile(token, msg.Document.FileID)
        data, err := downloadFile(token, fp)
        if err != nil { sendMessage(token, chatID, "❌ Ошибка загрузки", nil); return }
        if err := validateFile(data); err != nil { sendMessage(token, chatID, "❌ "+err.Error(), nil); return }
        cv := extractTextFromPDF(data)
        if len(cv) < 20 { sendMessage(token, chatID, "⚠️ Не удалось прочитать PDF", nil); return }
        prof := detectProfession(cv)
        sessionMutex.Lock()
        userSessions[chatID] = &UserSession{State: "awaiting_profession", CVText: cv, Profession: prof, ChatID: chatID, LastActivity: time.Now()}
        sessionMutex.Unlock()
        sendMessage(token, chatID, fmt.Sprintf("✅ <b>%s</b>\nВыбери профессию:", prof), professionKeyboard(prof))
        return
    }

    if msg.Text != "" {
        sessionMutex.RLock()
        s := userSessions[chatID]
        sessionMutex.RUnlock()
        if s == nil { sendMessage(token, chatID, "Нажми /start", nil); return }
        switch msg.Text {
        case "🔍 Новый поиск":
            s.State = "awaiting_cv"; sendMessage(token, chatID, "Отправь PDF", removeKeyboard()); return
        }
        switch s.State {
        case "awaiting_profession":
            s.Profession = strings.TrimPrefix(msg.Text, "✅ ")
            s.State = "awaiting_country"
            sendMessage(token, chatID, "🌍 Страна:", countryKeyboard())
        case "awaiting_country":
            country := cleanCountry(msg.Text)
            s.Country = country; s.State = "browsing"
            prof := s.Profession
            sendMessage(token, chatID, fmt.Sprintf("🔍 Ищу <b>%s</b> в <b>%s</b>...", prof, country), nil)
            jobs := js.searchAll(prof, country)
            if len(jobs) == 0 { sendMessage(token, chatID, "❌ Ничего не найдено", mainKeyboard()); return }
            s.CurrentJobs = jobs; s.JobIndex = 0
            sendMessage(token, chatID, fmt.Sprintf("✅ Найдено <b>%d</b>:", len(jobs)), nil)
            showCurrentJob(token, s, cfg.DeepSeekAPIKey)
        }
    }
}

func cleanCountry(s string) string {
    for _, f := range []string{"🇳🇱 ", "🇩🇪 ", "🇬🇧 ", "🇺🇸 ", "🇨🇦 ", "🇫🇷 "} { s = strings.TrimPrefix(s, f) }
    return s
}

func showCurrentJob(token string, s *UserSession, apiKey string) {
    if s.JobIndex >= len(s.CurrentJobs) { sendMessage(token, s.ChatID, "🏁 Всё просмотрено!", mainKeyboard()); return }
    j := s.CurrentJobs[s.JobIndex]
    desc := translateText(apiKey, j.Description)
    txt := fmt.Sprintf("<b>%d/%d</b>  %s\n🏢 %s\n📍 %s\n💰 %s\n📝 %s\n🔗 <a href=\"%s\">Открыть вакансию</a>",
        s.JobIndex+1, len(s.CurrentJobs), j.Title, j.Company, j.Location, j.Salary, desc, j.URL)
    sendMessage(token, s.ChatID, txt, jobActionKeyboard(s.JobIndex))
}
