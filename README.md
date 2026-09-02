# 🤖 Копейка Бот

Telegram-бот для учёта личных финансов: расходы, доходы, копилки, цели, AI-советы.

## Возможности
- Учёт расходов/доходов по категориям
- Копилки с целями
- Семейный бюджет
- AI-советы по экономии (DeepSeek)
- Экспорт данных
- Подписки (Premium/Business)

## Переменные окружения

Создай `.env` на основе `.env.example`:

| Переменная | Описание | Обязательно |
|------------|----------|-------------|
| `TELEGRAM_BOT_TOKEN` | Токен от @BotFather | Да |
| `DEEPSEEK_API_KEY` | Ключ DeepSeek | Да |
| `WEBHOOK_URL` | URL для webhook (например `https://kopeyka-bot.fly.dev`) | Да |
| `WEBHOOK_PORT` | Порт (по умолчанию 8080) | Нет |
| `DATABASE_PATH` | Путь к SQLite (по умолчанию `/data/kopeyka.db`) | Нет |
| `ADMIN_CHAT_ID` | Telegram ID админа | Нет |
| `YOOKASSA_SHOP_ID` | ЮKassa Shop ID | Нет |
| `YOOKASSA_SECRET_KEY` | ЮKassa Secret | Нет |
| `STRIPE_SECRET_KEY` | Stripe Secret | Нет |

## Деплой на Fly.io (бесплатно)

```bash
# 1. Установи flyctl
curl -L https://fly.io/install.sh | sh

# 2. Логин
fly auth login

# 3. Создай приложение
fly apps create kopeyka-bot

# 4. Создай volume для SQLite (1GB бесплатно)
fly volumes create kopeyka_data --size 1 --region waw

# 5. Задай секреты
fly secrets set TELEGRAM_BOT_TOKEN="your_token"
fly secrets set DEEPSEEK_API_KEY="your_key"
fly secrets set WEBHOOK_URL="https://kopeyka-bot.fly.dev"

# 6. Деплой
fly deploy

# 7. Установи webhook в Telegram
curl "https://api.telegram.org/bot<YOUR_TOKEN>/setWebhook?url=https://kopeyka-bot.fly.dev/<YOUR_TOKEN>"
