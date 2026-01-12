# 📈 Financial Market Assistant Bot

A Go-based Telegram bot for financial market data, news, and broadcasting.

---

## 🚀 Key Features

-   **💹 Real-time Tracking:** Fetches market prices (forex, gold, crypto) from the [Twelve Data API](https://twelvedata.com/).
-   **📰 News Aggregation & Translation:** Pulls RSS news from [Investing.com](https://www.investing.com/rss/) and translates headlines to Vietnamese using a Google Apps Script endpoint.
-   **☁️ Serverless-Ready:** Dual-mode operation:
    -   **Local:** Runs as a long-polling bot for development and testing.
    -   **AWS Lambda:** Deployable as a serverless webhook for production.
-   **💾 Persistent Storage:** Stores subscribed user chat IDs securely in [MongoDB Atlas](https://www.mongodb.com/cloud/atlas).

---

## 🏗️ Quick Start — Local (Development)

1.  **Prerequisites:** Ensure you have **Go (1.25.x or later)** installed.
2.  **Configuration:** Copy `.env.example` to `.env` and fill in your API keys and secrets.
3.  **Run:** Fetch dependencies and start the bot:

    ```bash
    go mod tidy
    go run main.go
    ```

    In local mode, the bot uses a cron job (1-minute interval) for market updates and responds to `/start` and `/update` commands on Telegram.

---

## 🛠️ Environment Variables

Create a `.env` file from the `.env.example` template. **Do not commit this file to source control.**

| Variable              | Description                                        | Required |
| --------------------- | -------------------------------------------------- | :------: |
| `TELEGRAM_TOKEN`      | Your Telegram bot token.                           |  **Yes** |
| `TWELVE_DATA_API_KEY` | API key for the Twelve Data service.               |  **Yes** |
| `GOOGLE_SCRIPT_URL`   | Google Apps Script URL for translation.            |  **Yes** |
| `MONGODB_URI`         | MongoDB Atlas connection string.                   |  **Yes** |
| `MODE`                | Set to `lambda` for webhook mode. (Defaults to `local`) |    No    |

---

## 📦 Deploying to AWS Lambda

1.  **Build:** Compile a Linux-compatible binary:

    ```powershell
    # For Windows (PowerShell)
    $env:GOOS='linux'; $env:GOARCH='amd64'; go build -o bootstrap main.go
    ```

    ```bash
    # For macOS/Linux
    GOOS=linux GOARCH=amd64 go build -o bootstrap main.go
    ```

2.  **Package:** Zip the compiled `bootstrap` binary into `bootstrap.zip`.
3.  **Deploy:** Upload `bootstrap.zip` to your AWS Lambda function and configure it to use a custom `provided.al2` runtime.
4.  **Webhook:** Set up an API Gateway or Lambda Function URL to act as the webhook endpoint for Telegram.

---

## 📂 Project Structure

```
.
├── 📜 .env.example      # Environment variable template
├── 🚫 .gitignore        # Git ignore rules
├── 📦 go.mod             # Go module definitions
├── 🔒 go.sum             # Dependency checksums
├── 🚀 main.go            # Main application entry point (Lambda + local)
└── 📖 README.md          # This file
```

---

## 📝 Notes & Recommendations

-   **🔌 Client Management:** Consider creating singleton instances for the Database and Telegram clients to improve connection management and add graceful shutdown handlers.
-   **🛡️ Robustness:** Implement retries and timeouts for all external API calls (`Twelve Data`, `Google Script`).
-   **🧵 Concurrency:** Protect shared variables (like caches) with mutexes or other synchronization primitives.
-   **✅ Testing & CI:** Add unit tests, a linter (e.g., `golangci-lint`), and a basic CI/CD pipeline (e.g., GitHub Actions) to automate builds and testing.

---

## 🤝 Contributing

Contributions are welcome! Please feel free to open an issue or submit a pull request.

1.  Fork the repository.
2.  Create your feature branch (`git checkout -b feature/AmazingFeature`).
3.  Commit your changes (`git commit -m 'Add some AmazingFeature'`).
4.  Run `gofmt` to format your code.
5.  Push to the branch (`git push origin feature/AmazingFeature`).
6.  Open a Pull Request.

---

## 📄 License

This project does not yet contain a license. Please add one if you intend for it to be reused or distributed.