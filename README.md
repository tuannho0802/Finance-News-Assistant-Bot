# 📈 Financial Market Assistant Bot

A robust Go-based Telegram bot designed for real-time financial market tracking, news aggregation, and automated broadcasting. Optimized for both local development and production deployment on AWS Lambda.

---

## 🚀 Key Features

-   **💹 Real-time Market Data**: Fetches live prices for Forex (USD/VND, EUR/USD), Gold (XAU/USD), and Crypto (BTC/USD) via Twelve Data API.
-   **📰 Smart News Aggregator**: Pulls latest financial news from Investing.com with automatic Vietnamese translation via Google Apps Script.
-   **☁️ Serverless Optimized**: Designed to run seamlessly on AWS Lambda using Function URLs and Webhooks.
-   **💾 Database Integration**: Persistent user subscription management using MongoDB Atlas.
-   **🤖 Interactive UI**: Features inline buttons for instant price updates and markdown-formatted reports.

---

## 🛠️ Environment Variables

Configure these variables in your environment or a `.env` file for local testing. In Production (AWS Lambda), set these in the Lambda Configuration console.

| Variable              | Description                                        | Required |
| --------------------- | -------------------------------------------------- | :------: |
| `TELEGRAM_TOKEN`      | Telegram Bot Token from @BotFather.                |   Yes    |
| `TWELVE_DATA_API_KEY` | API key from Twelve Data for market quotes.        |   Yes    |
| `MONGODB_URI`         | MongoDB Atlas connection string.                   |   Yes    |
| `GOOGLE_SCRIPT_URL`   | URL of the Google Apps Script for translation.     |   Yes    |

---

## 🏗️ Quick Start — Local Development

**Prerequisites**: Install Go 1.21+.

**1. Setup:**

```bash
cp .env.example .env
# Fill in your secrets in .env
```

**2. Execution:**

```bash
go mod tidy
go run main.go
```

*In local mode, the bot uses Long Polling to listen for commands.*

---

## 🚀 CI/CD & Deployment (GitHub Actions)

This project is configured for automated deployment to AWS Lambda.

### 1. GitHub Secrets Setup

Ensure the following secrets are added to your GitHub repository (`Settings > Secrets and variables > Actions`):

-   `AWS_ACCESS_KEY_ID` & `AWS_SECRET_ACCESS_KEY`
-   `AWS_REGION` (e.g., `us-east-1`)
-   `LAMBDA_FUNCTION_NAME`
-   `LAMBDA_FUNCTION_URL` (The public URL of your Lambda)
-   `TELEGRAM_TOKEN`

### 2. Deployment Workflow

On every push to the `main` branch:

1.  The code is compiled for `linux/amd64`.
2.  A `bootstrap` binary is zipped and uploaded to AWS Lambda.
3.  The Telegram Webhook is automatically updated to point to your `LAMBDA_FUNCTION_URL`.

---

## 📂 Project Structure

```
.
├── .github/workflows/
│   └── deploy.yml        # CI/CD pipeline configuration
├── main.go               # Unified entry point (Lambda Handler + Local Poller)
├── go.mod                # Dependency management
├── .env.example          # Template for environment variables
└── README.md             # Documentation
```

---

## 📝 Technical Implementation Details

-   **Lambda Handler**: Uses `events.LambdaFunctionURLRequest` to handle both Webhook updates and empty-body triggers (for Cron broadcasting).
-   **Concurrency**: Uses `Synchronous: true` in production to ensure sequential processing within the short-lived Lambda execution environment.
-   **Caching**: Implements a 6-hour memory cache for USD/VND rates to optimize API credit usage.
-   **Database**: Uses a 10-second connection timeout to prevent hanging during cold starts.

---

## 📄 License

MIT License - Feel free to use and modify for your own projects.
