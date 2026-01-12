package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/mmcdole/gofeed"
	"github.com/robfig/cron/v3"
	tele "gopkg.in/telebot.v3"
)

// Global variables for price caching and user management
var (
	cachedUsdVnd    float64
	lastCacheUpdate time.Time
	cacheDuration   = 6 * time.Hour
	userFile        = "users.txt"
)

// PriceResponse represents the standard JSON structure from Twelve Data API
type PriceResponse struct {
	Price   string `json:"price"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- USER MANAGEMENT LOGIC ---

// loadUsers reads unique Telegram chat IDs from the local storage file
func loadUsers() map[int64]bool {
	users := make(map[int64]bool)
	file, err := os.Open(userFile)
	if err != nil {
		return users
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		id, _ := strconv.ParseInt(scanner.Text(), 10, 64)
		if id != 0 {
			users[id] = true
		}
	}
	return users
}

// saveUser appends a new Telegram chat ID to the storage file if it does not exist
func saveUser(id int64) {
	users := loadUsers()
	if _, exists := users[id]; !exists {
		file, err := os.OpenFile(userFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Printf("[ERROR] Không thể lưu user: %v",
				err)
			return
		}
		defer file.Close()
		fmt.Fprintf(file, "%d\n", id)
		log.Printf("[SYSTEM] Đã đăng ký người dùng mới: %d", id)
	}
}

// --- MARKET DATA & API LOGIC ---

// getPrice retrieves the current price for a given symbol using the Twelve Data API
func getPrice(symbol string, apiKey string) (float64, error) {
	apiUrl := fmt.Sprintf("https://api.twelvedata.com/price?symbol=%s&apikey=%s", symbol, apiKey)
	resp, err := http.Get(apiUrl)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result PriceResponse
	json.NewDecoder(resp.Body).Decode(&result)

	// Check for API rate limits or credit exhaustion
	if result.Code == 429 || strings.Contains(strings.ToLower(result.Message), "credits") {
		return 0, fmt.Errorf("API_LIMIT_EXCEEDED")
	}
	p, _ := strconv.ParseFloat(result.Price, 64)
	return p, nil
}

// getCachedUsdVnd provides the USD/VND rate from cache or fetches new data if expired
func getCachedUsdVnd(apiKey string) (float64, error) {
	if time.Since(lastCacheUpdate) < cacheDuration && cachedUsdVnd > 0 {
		return cachedUsdVnd, nil
	}
	rate, err := getPrice("USD/VND", apiKey)
	if err != nil {
		return 0, err
	}
	cachedUsdVnd = rate
	lastCacheUpdate = time.Now()
	return rate, nil
}

// translateToVietnamese leverages a Google Apps Script bridge to translate news headlines
func translateToVietnamese(text string) string {
	scriptURL := os.Getenv("GOOGLE_SCRIPT_URL")
	apiURL := fmt.Sprintf("%s?text=%s&source=en&target=vi", scriptURL, url.QueryEscape(text))
	resp, _ := http.Get(apiURL)
	if resp == nil {
		return text
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	translated := string(body)
	if strings.Contains(translated, "<html") {
		return text
	}
	return translated
}

// formatVnd converts a float value into a formatted currency string with thousand separators
func formatVnd(val float64) string {
	str := fmt.Sprintf("%.0f", val)
	var result []string
	for i := len(str); i > 0; i -= 3 {
		start := i - 3
		if start < 0 {
			start = 0
		}
		result = append([]string{str[start:i]}, result...)
	}
	return strings.Join(result, ".")
}

// getMarketUpdate aggregates financial data and news headlines into a single formatted report
func getMarketUpdate() string {
	godotenv.Load()
	apiKey := os.Getenv("TWELVE_DATA_API_KEY")
	now := time.Now()
	dateStr := now.Format("02/01/2006")

	// Fetch current market prices
	pGold, _ := getPrice("XAU/USD", apiKey)
	pEUR, _ := getPrice("EUR/USD", apiKey)
	pBTC, _ := getPrice("BTC/USD", apiKey)
	usdToVnd, _ := getCachedUsdVnd(apiKey)

	// Validate data availability
	if pGold == 0 {
		return fmt.Sprintf("📅 **Bản tin [%s]**\n⚠️ Hệ thống đang bảo trì hoặc hết API credits.", dateStr)
	}

	// Parse financial news feed
	fp := gofeed.NewParser()
	feed, _ := fp.ParseURL("https://www.investing.com/rss/news_25.rss")
	newsList := ""
	if feed != nil {
		for i, item := range feed.Items {
			if i >= 7 {
				break
			}
			viTitle := translateToVietnamese(item.Title)
			newsList += fmt.Sprintf("🔹 **%s**\n🔗 [Xem chi tiết](%s)\n\n", viTitle, item.Link)
		}
	}

	// Build the final Telegram message content
	return fmt.Sprintf(
		"📅 **Nhịp Đập Thị Trường [%s]**\n"+
			"━━━━━━━━━━━━━━━━━━\n\n"+
			"🔴 **TIN TỨC QUAN TRỌNG:**\n\n%s"+
			"📈 **XU HƯỚNG THỊ TRƯỜNG:**\n"+
			"• Tỷ giá USD/VND: 1$ ≈ **%s VNĐ**\n"+
			"• Vàng (XAUUSD): $%.2f ≈ **%s VNĐ**\n"+
			"• EURUSD: %.4f ≈ **%s VNĐ**\n"+
			"• Bitcoin: $%.2f ≈ **%s VNĐ**\n\n"+
			"🎯 **VÙNG KỸ THUẬT:**\n"+
			"• Quan sát vùng Supply/Demand tại: **$%.2f**\n\n"+
			"━━━━━━━━━━━━━━━━━━\n"+
			"💡 *Gõ `/update` để cập nhật dữ liệu mới nhất.*",
		dateStr, newsList, formatVnd(usdToVnd), pGold, formatVnd(pGold*usdToVnd),
		pEUR, formatVnd(pEUR*usdToVnd), pBTC, formatVnd(pBTC*usdToVnd), pGold,
	)
}

// --- MAIN EXECUTION ---

func main() {
	// Initialize environment variables and bot settings
	godotenv.Load()
	token := os.Getenv("TELEGRAM_TOKEN")

	b, err := tele.NewBot(tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	})
	if err != nil {
		log.Fatal(err)
	}

	// Setup task scheduler with ICT (UTC+7) timezone
	location := time.FixedZone("ICT", 7*3600)
	c := cron.New(cron.WithLocation(location))

	// Test Job: Broadcast market updates every 1 minute
	c.AddFunc("*/1 * * * *", func() {
		users := loadUsers()
		if len(users) == 0 {
			log.Println("[TEST-CRON] Không có user nào để gửi tin.")
			return
		}

		msg := getMarketUpdate()
		log.Printf("[TEST-CRON] Đang gửi test cho %d người dùng...", len(users))

		for id := range users {
			_, err := b.Send(&tele.Chat{ID: id}, msg, &tele.SendOptions{ParseMode: tele.ModeMarkdown, DisableWebPagePreview: true})
			if err != nil {
				log.Printf("[TEST-CRON ERROR] Lỗi gửi cho ID %d: %v", id, err)
			}
		}
	})

	// Daily Job: Broadcast market updates at 08:00 AM daily
	c.AddFunc("0 8 * * *", func() {
		users := loadUsers()
		if len(users) == 0 {
			return
		}

		msg := getMarketUpdate()
		log.Printf("[CRON] Bắt đầu gửi bản tin cho %d người dùng...", len(users))

		for id := range users {
			b.Send(&tele.Chat{ID: id}, msg, &tele.SendOptions{ParseMode: tele.ModeMarkdown, DisableWebPagePreview: true})
		}
	})

	c.Start()

	// Handler for /start command: Registers the user and sends a welcome message
	b.Handle("/start", func(c tele.Context) error {
		saveUser(c.Chat().ID)
		return c.Send("Chào mừng Trader! Bạn đã đăng ký nhận bản tin 8:00 sáng hàng ngày.\n\nGõ `/update` để xem ngay.")
	})

	// Handler for /update command: Provides an on-demand market report
	b.Handle("/update", func(c tele.Context) error {
		return c.Send(getMarketUpdate(), &tele.SendOptions{ParseMode: tele.ModeMarkdown, DisableWebPagePreview: true})
	})

	// Launch the bot
	log.Printf("[SYSTEM] Bot đa người dùng đang chạy...")
	b.Start()
}
