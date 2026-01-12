**Project Overview**

This repository contains a small Go-based Telegram bot that fetches financial market data (forex, gold, crypto), translates macroeconomic news into Vietnamese, and broadcasts market summaries to subscribed Telegram users. It supports both local long-polling and an AWS Lambda webhook mode.

**Key Features**

- **Real-time Tracking:** Fetches market prices from the Twelve Data API.
- **News Aggregation & Translation:** Pulls RSS news (Investing.com) and translates headlines via a Google Apps Script endpoint.
- **Serverless-ready:** Can run as an AWS Lambda function (webhook) or as a local long-polling bot.
- **Persistent Storage:** Stores subscribed user chat IDs in MongoDB Atlas.

**Quick Start — Local (Development)**

1. Ensure you have Go installed (the project targets Go 1.25.x).
2. Copy `.env.example` to `.env` and populate the variables.
3. Fetch modules and run locally:

```bash
go mod tidy
go run main.go
```

In local mode the bot also runs a cron job (1 minute by default) for quick testing and will respond to `/start` and `/update` commands when you connect via Telegram.

**Environment Variables**

Copy and edit `.env.example` with your secrets. At minimum set:

- `TELEGRAM_TOKEN` — Telegram bot token
- `TWELVE_DATA_API_KEY` — API key for market prices
- `GOOGLE_SCRIPT_URL` — Google Apps Script URL for translation
- `MONGODB_URI` — MongoDB Atlas connection string

Do not commit your `.env` file or credentials to source control.

**Deploying to AWS Lambda**

Build a Linux-compatible binary and upload as a custom runtime `bootstrap`:

```powershell
$env:GOOS='linux'; $env:GOARCH='amd64'; go build -o bootstrap main.go
# Zip `bootstrap` into `bootstrap.zip` and upload to AWS Lambda
```

Configure an API Gateway or Lambda Function URL (webhook) and set `AWS_LAMBDA_FUNCTION_NAME` in the environment when testing in Lambda.

**Project Structure**

- [main.go](main.go): Application entry point — contains Lambda handler and local long-polling mode.
- [go.mod](go.mod): Module and dependency definitions.
- [.env.example](.env.example): Environment variable template.

**Notes & Recommendations**

- The bot currently creates DB and Telegram clients inside runtime paths; consider converting these to singletons and adding graceful shutdown / disconnects for production.
- Add retries and timeouts for external HTTP/API calls and protect shared cache variables with synchronization primitives.
- Add unit tests, a linter (e.g. `golangci-lint`) and a simple CI workflow for builds.

**Contributing**

Contributions are welcome. Please open an issue or submit a pull request. For code changes, add unit tests and run `gofmt`.

**License**

This project does not include a license file. Add one if you intend to allow reuse.
