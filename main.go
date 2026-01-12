package main

import (
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

type PriceResponse struct {
	Price   string `json:"price"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Fetches the current price of a symbol using Twelve Data API
func getPrice(symbol string, apiKey string) (float64, error) {
	url := fmt.Sprintf("https://api.twelvedata.com/price?symbol=%s&apikey=%s", symbol, apiKey)
	resp, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result PriceResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	if result.Code == 429 || strings.Contains(strings.ToLower(result.Message), "credits") {
		return 0, fmt.Errorf("API_LIMIT_EXCEEDED")
	}

	p, _ := strconv.ParseFloat(result.Price, 64)
	return p, nil
}

// Translates text via Google Apps Script Bridge
func translateToVietnamese(text string) string {
	scriptURL := os.Getenv("GOOGLE_SCRIPT_URL")
	if scriptURL == "" {
		return text
	}

	apiURL := fmt.Sprintf("%s?text=%s&source=en&target=vi", scriptURL, url.QueryEscape(text))
	resp, err := http.Get(apiURL)
	if err != nil {
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

// Formats a float to a currency string with dots for thousands
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

// Generates the daily market pulse report
func getMarketUpdate() string {
	godotenv.Load()
	apiKey := os.Getenv("TWELVE_DATA_API_KEY")

	now := time.Now()
	dateStr := now.Format("02/01/2006")

	// Dynamic Data Acquisition
	pGold, errG := getPrice("XAU/USD", apiKey)
	pEUR, errE := getPrice("EUR/USD", apiKey)
	pBTC, errB := getPrice("BTC/USD", apiKey)
	// Fetching live USD/VND rate instead of hardcoded value
	usdToVnd, errV := getPrice("USD/VND", apiKey)

	// Check for API limits
	if errG != nil || errE != nil || errB != nil || errV != nil {
		errorMessage := "⚠️ Hết API credits TwelveData cho hôm nay hoặc lỗi kết nối tỷ giá."
		log.Println(errorMessage)
		return fmt.Sprintf("📅 **Nhịp Đập Thị Trường [%s]**\n\n%s", dateStr, errorMessage)
	}

	// News Acquisition
	fp := gofeed.NewParser()
	feed, err := fp.ParseURL("https://www.investing.com/rss/news_25.rss")
	newsList := ""
	if err == nil {
		log.Println("--- Đang dịch tin tức từ Google Apps Script ---")
		for i, item := range feed.Items {
			if i >= 3 {
				break
			}
			viTitle := translateToVietnamese(item.Title)
			newsList += fmt.Sprintf("• [%s](%s)\n", viTitle, item.Link)
		}
	}

	// Calculate Display Values
	goldVnd := formatVnd(pGold * usdToVnd)
	eurVnd := formatVnd(pEUR * usdToVnd)
	btcVnd := formatVnd(pBTC * usdToVnd)

	return fmt.Sprintf(
		"📅 **Nhịp Đập Thị Trường [%s]**\n\n"+
			"🔴 **Tin Tức Quan Trọng:**\n%s\n"+
			"📈 **Xu Hướng Thị Trường:**\n"+
			"• **Tỷ giá USD/VND:** 1$ ≈ **%s VNĐ**\n"+
			"• **Vàng (XAUUSD):** $%.2f ≈ **%s VNĐ/oz**\n"+
			"• **EURUSD:** %.4f ≈ **%s VNĐ**\n"+
			"• **Bitcoin:** $%.2f ≈ **%s VNĐ/BTC**\n\n"+
			"🎯 **Vùng Kỹ Thuật:**\n"+
			"• Dựa trên Price Action, quan sát vùng: $%.2f\n\n"+
			"💡 **IT/EA Tip:**\n"+
			"Tỷ giá USD/VND được cập nhật real-time giúp EA tính toán Lot size chính xác hơn khi quản lý vốn bằng VNĐ.",
		dateStr,
		newsList,
		formatVnd(usdToVnd),
		pGold, goldVnd,
		pEUR, eurVnd,
		pBTC, btcVnd,
		pGold,
	)
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	token := os.Getenv("TELEGRAM_TOKEN")
	myChatIDStr := os.Getenv("MY_CHAT_ID")
	myChatID, _ := strconv.ParseInt(myChatIDStr, 10, 64)

	b, err := tele.NewBot(tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	})
	if err != nil {
		log.Fatalf("Failed to start bot: %v", err)
		return
	}

	location := time.FixedZone("ICT", 7*3600)
	c := cron.New(cron.WithLocation(location))

	c.AddFunc("*/1 * * * *", func() {
		log.Printf("Executing 1-minute test broadcast to ID: %d", myChatID)
		if myChatID != 0 {
			msg := getMarketUpdate()
			b.Send(&tele.Chat{ID: myChatID}, msg, &tele.SendOptions{ParseMode: tele.ModeMarkdown, DisableWebPagePreview: true})
		}
	})

	c.AddFunc("0 8 * * *", func() {
		log.Println("Executing scheduled 8:00 AM update")
		if myChatID != 0 {
			b.Send(&tele.Chat{ID: myChatID}, getMarketUpdate(), &tele.SendOptions{ParseMode: tele.ModeMarkdown, DisableWebPagePreview: true})
		}
	})

	c.Start()
	log.Println("Cron scheduler started successfully")

	b.Handle("/update", func(c tele.Context) error {
		log.Printf("Manual update triggered by %s", c.Sender().Username)
		return c.Send(getMarketUpdate(), &tele.SendOptions{ParseMode: tele.ModeMarkdown, DisableWebPagePreview: true})
	})

	b.Handle("/myid", func(c tele.Context) error {
		return c.Send(fmt.Sprintf("ID của bạn: %d", c.Chat().ID))
	})

	log.Printf("Bot is giờ đang chạy. Theo dõi ID: %d...", myChatID)
	b.Start()
}
