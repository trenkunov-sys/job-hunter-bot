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
    Title           string
    Company         string
    Location        string
    Description     string
    URL             string // Прямая ссылка на сайт работодателя
    ApplyURL        string // Ссылка для отклика (если есть)
    Salary          string
    HasTest         bool
    Source          string
    Stack           string
    Language        string // Язык вакансии (en, de, nl, fr)
    ContactInfo     string // Контакты рекрутера/HR (если есть)
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
    if apiKey == "" { return "", fmt.Errorf("API ключ не задан") }
    reqBody := map[string]interface{}{
        "model": "deepseek-chat", "temperature": 0.7, "max_tokens": 800,
        "messages": []map[string]string{{"role": "user", "content": prompt}},
    }
    jsonData, _ := json.Marshal(reqBody)
    req, _ := http.NewRequest("POST", "https://api.deepseek.com/v1/chat/completions", bytes.NewBuffer(jsonData))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+apiKey)
    client := &http.Client{Timeout: 45 * time.Second}
    resp, err := client.Do(req)
    if err != nil { return "", err }
    defer resp.Body.Close()
    body, _ := io.ReadAll(resp.Body)
    var result struct { Choices []struct { Message struct{ Content string }`json:"message"` }`json:"choices"` }
    json.Unmarshal(body, &result)
    if len(result.Choices) > 0 { return result.Choices[0].Message.Content, nil }
    return "", fmt.Errorf("пустой ответ")
}

const MaxFileSize = 5 * 1024 * 1024
func validateFile(data []byte) error {
    if len(data) > MaxFileSize { return fmt.Errorf("файл превышает 5 МБ") }
    return nil
}

func extractTextFromPDF(data []byte) string {
    var result strings.Builder
    for i := 0; i < len(data); i++ {
        if data[i] == '(' {
            start, end := i+1, i+1
            for end < len(data) && data[end] != ')' { end++ }
            if end > start {
                chunk := string(data[start:end])
                if utf8.ValidString(chunk) && len(chunk) > 2 { result.WriteString(chunk + " ") }
            }
            i = end
        }
    }
    text := result.String()
    if len(text) < 50 { return string(data) }
    return strings.Join(strings.Fields(text), " ")
}

func detectProfession(cvText string) string {
    cvLower := strings.ToLower(cvText)
    analystWords := []string{"бизнес-аналитик", "business analyst", "системный аналитик", "system analyst", "аналитик", "use case", "user story", "bpmn", "uml", "rest", "soap", "kafka", "sql", "postgresql", "oracle", "cassandra", "confluence", "jira", "swagger", "postman", "сбор требований", "анализ требований", "техническое задание", "спецификации", "декомпозиция"}
    analystScore := 0
    for _, w := range analystWords { if strings.Contains(cvLower, w) { analystScore += 10 } }
    if analystScore >= 30 { return "Бизнес/Системный аналитик" }
    keywords := map[string][]string{
        "Golang Developer": {"golang", "go ", "goroutine"}, "Python Developer": {"python", "django", "flask"},
        "Java Developer": {"java", "spring", "hibernate"}, "JavaScript Developer": {"javascript", "node.js", "react"},
        "DevOps Engineer": {"devops", "kubernetes", "docker", "aws"}, "QA Engineer": {"qa ", "testing", "selenium", "тестировщик"},
        "Product Manager": {"product manager", "agile", "scrum"}, "Data Scientist": {"data science", "machine learning", "tensorflow"},
    }
    bestMatch, maxScore := "Software Developer", 0
    for prof, words := range keywords {
        score := 0
        for _, w := range words { if strings.Contains(cvLower, w) { score += 10 } }
        if score > maxScore && score > analystScore { maxScore, bestMatch = score, prof }
    }
    if maxScore > analystScore { return bestMatch }
    return "Бизнес/Системный аналитик"
}

func stripHTML(html string) string {
    var result strings.Builder; inTag := false
    for _, r := range html {
        if r == '<' { inTag = true; continue }
        if r == '>' { inTag = false; result.WriteRune(' '); continue }
        if !inTag { result.WriteRune(r) }
    }
    return strings.Join(strings.Fields(result.String()), " ")
}

// getLanguageByCountry возвращает язык для письма в зависимости от страны
func getLanguageByCountry(country string) string {
    switch strings.ToLower(country) {
    case "germany", "германия": return "de"
    case "netherlands", "нидерланды": return "nl"
    case "france", "франция": return "fr"
    default: return "en"
    }
}

type JobSearcher struct { adzunaAppID, adzunaAppKey string }
func newJobSearcher(cfg *Config) *JobSearcher { return &JobSearcher{cfg.AdzunaAppID, cfg.AdzunaAppKey} }

func (s *JobSearcher) searchAll(profession, country string) []Job {
    var allJobs []Job
    searchTerm := s.mapProfessionToSearchTerm(profession)
    allJobs = append(allJobs, s.searchHimalayas(searchTerm, country)...)
    if s.adzunaAppID != "" && s.adzunaAppKey != "" { allJobs = append(allJobs, s.searchAdzuna(searchTerm, country)...) }
    if len(allJobs) == 0 { return s.getMockJobs(profession, country) }
    return allJobs
}

func (s *JobSearcher) mapProfessionToSearchTerm(p string) string {
    p = strings.ToLower(p)
    if strings.Contains(p, "analyst") || strings.Contains(p, "аналитик") { return "business analyst" }
    if strings.Contains(p, "golang") { return "golang developer" }
    if strings.Contains(p, "python") { return "python developer" }
    if strings.Contains(p, "qa") { return "qa engineer" }
    return p
}

func (s *JobSearcher) searchHimalayas(profession, country string) []Job {
    apiURL := fmt.Sprintf("https://himalayas.app/jobs/api?search=%s&limit=5", strings.ReplaceAll(profession, " ", "%20"))
    resp, err := http.Get(apiURL)
    if err != nil { return nil }
    defer resp.Body.Close()
    var result struct { Jobs []struct {
        ID, Title, CompanyName, Location, Description, URL string; SalaryMin, SalaryMax int; Currency string
        ApplicationURL string `json:"application_url"`
    }`json:"jobs"` }
    json.NewDecoder(resp.Body).Decode(&result)
    var jobs []Job
    for _, j := range result.Jobs {
        salary := ""
        if j.SalaryMin > 0 && j.SalaryMax > 0 { salary = fmt.Sprintf("%d - %d %s", j.SalaryMin, j.SalaryMax, j.Currency) }
        company := j.CompanyName; if company == "" { company = "Компания не указана" }
        desc := stripHTML(j.Description)
        if len(desc) > 400 { desc = desc[:400] + "..." }
        if desc == "" { desc = "Описание недоступно" }
        stack := extractStack(j.Description)
        // Используем application_url если есть, иначе url
        directURL := j.ApplicationURL
        if directURL == "" { directURL = j.URL }
        if directURL == "" { directURL = fmt.Sprintf("https://himalayas.app/jobs/%s", j.ID) }
        
        lang := getLanguageByCountry(country)
        if country == "Remote" || country == "" { lang = "en" }
        
        jobs = append(jobs, Job{
            Title: j.Title, Company: company, Location: j.Location,
            Description: desc, URL: directURL, ApplyURL: j.ApplicationURL,
            Salary: salary, Source: "Himalayas", Stack: stack,
            Language: lang,
            ContactInfo: fmt.Sprintf("Страница компании: https://himalayas.app/companies/%s", strings.ToLower(strings.ReplaceAll(company, " ", "-"))),
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
    apiURL := fmt.Sprintf("https://api.adzuna.com/v1/api/jobs/%s/search/1?app_id=%s&app_key=%s&what=%s&results_per_page=5",
        countryCode, s.adzunaAppID, s.adzunaAppKey, strings.ReplaceAll(profession, " ", "%20"))
    resp, err := http.Get(apiURL)
    if err != nil { return nil }
    defer resp.Body.Close()
    var result struct { Results []struct {
        Title, Description, RedirectURL string; SalaryMin, SalaryMax float64
        Company, Location struct{ DisplayName string }`json:"company" json:"location"`
    }`json:"results"` }
    json.NewDecoder(resp.Body).Decode(&result)
    var jobs []Job
    for _, j := range result.Results {
        salary := ""
        if j.SalaryMin > 0 && j.SalaryMax > 0 { salary = fmt.Sprintf("%.0f - %.0f", j.SalaryMin, j.SalaryMax) }
        company := j.Company.DisplayName; if company == "" { company = "Компания не указана" }
        desc := stripHTML(j.Description)
        if len(desc) > 400 { desc = desc[:400] + "..." }
        if desc == "" { desc = "Описание недоступно" }
        stack := extractStack(j.Description)
        directURL := j.RedirectURL
        if directURL == "" { directURL = fmt.Sprintf("https://www.adzuna.com/search?q=%s", strings.ReplaceAll(profession, " ", "+")) }
        
        lang := getLanguageByCountry(country)
        
        jobs = append(jobs, Job{
            Title: j.Title, Company: company, Location: j.Location.DisplayName,
            Description: desc, URL: directURL, ApplyURL: directURL,
            Salary: salary, Source: "Adzuna", Stack: stack,
            Language: lang,
            ContactInfo: fmt.Sprintf("Страница компании: %s", j.Company.DisplayName),
        })
    }
    return jobs
}

func (s *JobSearcher) getMockJobs(profession, country string) []Job {
    return []Job{
        {Title: fmt.Sprintf("Senior %s", profession), Company: "Tech Corp", Location: country, Description: "Разработка систем.", URL: "https://example.com/jobs/1", ApplyURL: "https://example.com/apply/1", Source: "Mock", Salary: "$80,000 - $100,000", Stack: "Go, PostgreSQL", Language: "en", ContactInfo: "hr@techcorp.com"},
        {Title: profession, Company: "Startup Inc", Location: country, Description: "Agile-команда.", URL: "https://example.com/jobs/2", ApplyURL: "https://example.com/apply/2", Source: "Mock", Salary: "$60,000 - $80,000", Stack: "Python, Django", Language: "en", ContactInfo: "jobs@startup.com"},
    }
}

func extractStack(html string) string {
    lower := strings.ToLower(html)
    techs := []string{"go", "golang", "python", "java", "javascript", "typescript", "react", "vue", "angular", "node.js", "django", "flask", "spring", "sql", "postgresql", "mysql", "mongodb", "rest", "soap", "graphql", "grpc", "docker", "kubernetes", "aws", "azure", "gcp", "kafka", "redis", "elasticsearch", "bpmn", "uml", "json", "xml", "swagger", "postman"}
    found := []string{}
    for _, tech := range techs {
        if strings.Contains(lower, tech) { found = append(found, tech) }
    }
    if len(found) == 0 { return "Не указан" }
    if len(found) > 5 { found = found[:5] }
    return strings.Join(found, ", ")
}

func translateText(apiKey, text string) string {
    if apiKey == "" || len(text) < 20 { return text }
    resp, err := callDeepSeek(apiKey, "Переведи на русский язык, сохраняя названия технологий и компаний: "+text)
    if err != nil { return text }
    return resp
}

func generateCoverLetter(apiKey, cvText, jobTitle, company, jobDescription, language string) string {
    if jobTitle == "" { jobTitle = "вакансию" }
    if company == "" || company == "Не указана" { company = "компанию" }
    
    // Определяем язык письма
    langName := "русском"
    switch language {
    case "en": langName = "английском"
    case "de": langName = "немецком"
    case "nl": langName = "нидерландском"
    case "fr": langName = "французском"
    }
    
    if apiKey == "" {
        if language == "de" {
            return fmt.Sprintf("Sehr geehrte Damen und Herren,\n\nich bewerbe mich auf die Stelle %s bei %s.\n\nMit freundlichen Grüßen", jobTitle, company)
        }
        return fmt.Sprintf("Dear Hiring Manager,\n\nI am applying for the %s position at %s.\n\nBest regards", jobTitle, company)
    }
    if len(cvText) > 800 { cvText = cvText[:800] }
    if len(jobDescription) > 500 { jobDescription = jobDescription[:500] }
    
    prompt := fmt.Sprintf(`Ты — карьерный консультант. Напиши персонализированное сопроводительное письмо на %s языке для отклика на вакансию.

Резюме кандидата:
%s

Описание вакансии:
%s

Письмо должно:
1. Начинаться с обращения к hiring-менеджеру на %s языке
2. Показать, что кандидат изучил компанию и вакансию
3. Связать конкретный опыт из резюме с требованиями вакансии (1-2 примера)
4. Быть убедительным, но не длинным (4-6 предложений)
5. Заканчиваться призывом к действию`, langName, cvText, jobDescription, langName)
    
    resp, err := callDeepSeek(apiKey, prompt)
    if err != nil {
        if language == "de" {
            return fmt.Sprintf("Sehr geehrte Damen und Herren,\n\nich bewerbe mich auf die Stelle %s bei %s.\n\nMit freundlichen Grüßen", jobTitle, company)
        }
        return fmt.Sprintf("Dear Hiring Manager,\n\nI am applying for the %s position at %s.\n\nBest regards", jobTitle, company)
    }
    return resp
}

func formatJobCard(job Job) string {
    var sb strings.Builder
    sb.WriteString(fmt.Sprintf("<b>%s</b>\n\n", job.Title))
    sb.WriteString(fmt.Sprintf("🏢 <b>%s</b>\n", job.Company))
    if job.Salary != "" { sb.WriteString(fmt.Sprintf("💰 %s\n", job.Salary)) }
    if job.Location != "" { sb.WriteString(fmt.Sprintf("📍 %s\n", job.Location)) }
    if job.Stack != "" && job.Stack != "Не указан" { sb.WriteString(fmt.Sprintf("🛠 Стек: %s\n", job.Stack)) }
    if job.Language != "" { sb.WriteString(fmt.Sprintf("🌐 Язык письма: %s\n", strings.ToUpper(job.Language))) }
    sb.WriteString(fmt.Sprintf("\n📝 %s\n", job.Description))
    
    // Ссылки и контакты
    sb.WriteString(fmt.Sprintf("\n🔗 <a href=\"%s\">Открыть вакансию на сайте работодателя</a>", job.URL))
    if job.ApplyURL != "" && job.ApplyURL != job.URL {
        sb.WriteString(fmt.Sprintf("\n✉️ <a href=\"%s\">Откликнуться напрямую</a>", job.ApplyURL))
    }
    if job.ContactInfo != "" {
        sb.WriteString(fmt.Sprintf("\n📞 %s", job.ContactInfo))
    }
    sb.WriteString(fmt.Sprintf("\n\n📌 Источник: %s", job.Source))
    return sb.String()
}

func professionKeyboard(detected string) map[string]interface{} {
    buttons := [][]map[string]interface{}{}
    if detected != "" { buttons = append(buttons, []map[string]interface{}{{"text": "✅ " + detected}}) }
    buttons = append(buttons, [][]map[string]interface{}{{{"text": "Golang Developer"}, {"text": "Python Developer"}}, {{"text": "QA Engineer"}, {"text": "Бизнес/Системный аналитик"}}, {{"text": "Product Manager"}, {"text": "DevOps Engineer"}}}...)
    return map[string]interface{}{"keyboard": buttons, "resize_keyboard": true}
}

func countryKeyboard() map[string]interface{} {
    return map[string]interface{}{"keyboard": [][]map[string]interface{}{{{"text": "🇳🇱 Netherlands"}, {"text": "🇩🇪 Germany"}}, {{"text": "🇬🇧 UK"}, {"text": "🇺🇸 USA"}}, {{"text": "🇨🇦 Canada"}, {"text": "🇫🇷 France"}}, {{"text": "🌍 Remote"}}}, "resize_keyboard": true}
}

func mainKeyboard() map[string]interface{} {
    return map[string]interface{}{"keyboard": [][]map[string]interface{}{{{"text": "🔍 Новый поиск"}, {"text": "💬 Обратная связь"}}, {{"text": "🆘 Помощь"}}}, "resize_keyboard": true}
}

func jobActionKeyboard(index int) map[string]interface{} {
    return map[string]interface{}{"inline_keyboard": [][]map[string]interface{}{{{"text": "✅ Откликнуться", "callback_data": fmt.Sprintf("apply_%d", index)}, {"text": "⏩ Пропустить", "callback_data": fmt.Sprintf("skip_%d", index)}}}}
}

func removeKeyboard() map[string]interface{} { return map[string]interface{}{"remove_keyboard": true} }

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
    port := os.Getenv("PORT"); if port == "" { port = "8080" }
    stop := make(chan os.Signal, 1); signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
    go func() { <-stop; os.Exit(0) }()
    log.Fatal(http.ListenAndServe(":"+port, nil))
}

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
    token := cfg.TelegramToken; js := newJobSearcher(cfg)
    if upd.CallbackQuery != nil {
        cq := upd.CallbackQuery; chatID := cq.Message.Chat.ID
        sessionMutex.RLock(); s := userSessions[chatID]; sessionMutex.RUnlock()
        if s == nil { answerCallback(token, cq.ID, "Сессия устарела"); return }
        parts := strings.SplitN(cq.Data, "_", 2)
        if len(parts) != 2 { return }
        idx, _ := strconv.Atoi(parts[1])
        if idx >= len(s.CurrentJobs) { answerCallback(token, cq.ID, "Вакансия недоступна"); return }
        job := s.CurrentJobs[idx]
        switch parts[0] {
        case "skip": s.JobIndex++; showCurrentJob(token, s, cfg.DeepSeekAPIKey)
        case "apply":
            letter := generateCoverLetter(cfg.DeepSeekAPIKey, s.CVText, job.Title, job.Company, job.Description, job.Language)
            // Письмо отправляется отдельным сообщением для удобства копирования
            sendMessage(token, chatID, letter, nil)
            s.JobIndex++; showCurrentJob(token, s, cfg.DeepSeekAPIKey)
        }
        return
    }
    if upd.Message == nil { return }
    msg := upd.Message; chatID := msg.Chat.ID
    username := msg.From.Username; if username == "" { username = msg.From.FirstName }
    if strings.HasPrefix(msg.Text, "/") {
        switch msg.Text {
        case "/start":
            sessionMutex.Lock(); userSessions[chatID] = &UserSession{State: "awaiting_cv", ChatID: chatID, LastActivity: time.Now()}; sessionMutex.Unlock()
            sendMessage(token, chatID, fmt.Sprintf("👋 Привет, @%s!\n\nОтправь PDF с резюме.", username), removeKeyboard())
        case "/reset":
            sessionMutex.Lock(); delete(userSessions, chatID); sessionMutex.Unlock()
            sendMessage(token, chatID, "🔄 Сброшено.", removeKeyboard())
        case "/help": sendMessage(token, chatID, "/start /reset /how /feedback\nСвязь: @Trene4ca", mainKeyboard())
        case "/how": sendMessage(token, chatID, "📋 Загрузи PDF → выбери страну → получи вакансии → нажми «Откликнуться». Бот напишет письмо на языке работодателя, отклик отправишь сам по ссылке.", mainKeyboard())
        case "/feedback": sendMessage(token, chatID, "💬 Напиши @Trene4ca:", removeKeyboard())
        }
        return
    }
    if msg.Document != nil {
        fp := getFile(token, msg.Document.FileID); data, err := downloadFile(token, fp)
        if err != nil { sendMessage(token, chatID, "❌ Ошибка загрузки", nil); return }
        if err := validateFile(data); err != nil { sendMessage(token, chatID, "❌ "+err.Error(), nil); return }
        cv := extractTextFromPDF(data)
        if len(cv) < 20 { sendMessage(token, chatID, "⚠️ Не удалось прочитать PDF", nil); return }
        prof := detectProfession(cv)
        sessionMutex.Lock(); userSessions[chatID] = &UserSession{State: "awaiting_profession", CVText: cv, Profession: prof, ChatID: chatID, LastActivity: time.Now()}; sessionMutex.Unlock()
        sendMessage(token, chatID, fmt.Sprintf("✅ <b>%s</b>\nВыбери профессию:", prof), professionKeyboard(prof))
        return
    }
    if msg.Text != "" {
        sessionMutex.RLock(); s := userSessions[chatID]; sessionMutex.RUnlock()
        if s == nil { sendMessage(token, chatID, "Нажми /start", nil); return }
        switch msg.Text { case "🔍 Новый поиск": s.State = "awaiting_cv"; sendMessage(token, chatID, "Отправь PDF", removeKeyboard()); return }
        switch s.State {
        case "awaiting_profession":
            s.Profession = strings.TrimPrefix(msg.Text, "✅ "); s.State = "awaiting_country"
            sendMessage(token, chatID, "🌍 Страна:", countryKeyboard())
        case "awaiting_country":
            country := cleanCountry(msg.Text); s.Country = country; s.State = "browsing"; prof := s.Profession
            sendMessage(token, chatID, fmt.Sprintf("🔍 Ищу <b>%s</b> в <b>%s</b>...", prof, country), nil)
            jobs := js.searchAll(prof, country)
            if len(jobs) == 0 { sendMessage(token, chatID, "❌ Ничего не найдено", mainKeyboard()); return }
            s.CurrentJobs = jobs; s.JobIndex = 0
            sendMessage(token, chatID, fmt.Sprintf("✅ Найдено <b>%d</b> вакансий:", len(jobs)), nil)
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
    j.Description = translateText(apiKey, j.Description)
    jobCard := formatJobCard(j)
    sendMessage(token, s.ChatID, jobCard, jobActionKeyboard(s.JobIndex))
}
