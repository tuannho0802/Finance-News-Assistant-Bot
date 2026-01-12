package main

import (
	"context"
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

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/joho/godotenv"
	"github.com/mmcdole/gofeed"
	"github.com/robfig/cron/v3"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	tele "gopkg.in/telebot.v3"
)

// Global variables
var (
	cachedUsdVnd    float64
	lastCacheUpdate time.Time
	cacheDuration   = 6 * time.Hour
	userCollection  *mongo.Collection
)

type PriceResponse struct {
	Price   string `json:"price"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- DATABASE LOGIC ---

func initDatabase() {
	uri := os.Getenv("MONGODB_URI")
	client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI(uri))
	if err != nil {
		log.Fatal(err)
	}
	userCollection = client.Database("market_bot").Collection("users")
	log.Println("[DATABASE] Connected to MongoDB Atlas")
}

func loadUsers() map[int64]bool {
	users := make(map[int64]bool)
	cursor, err := userCollection.Find(context.TODO(), bson.M{})
	if err != nil {
		log.Printf("[DATABASE ERROR] Failed to find users: %v", err)
		return users
	}
	defer cursor.Close(context.TODO())

	for cursor.Next(context.TODO()) {
		var result struct {
			ChatID int64 `bson:"chat_id"`
		}
		cursor.Decode(&result)
		users[result.ChatID] = true
	}
	return users
}

func saveUser(id int64) {
	filter := bson.M{"chat_id": id}
	update := bson.M{"$set": bson.M{"chat_id": id, "updated_at": time.Now()}}
	_, err := userCollection.UpdateOne(context.TODO(), filter, update, options.Update().SetUpsert(true))
	if err != nil {
		log.Printf("[DATABASE ERROR] Failed to save user %d: %v", id, err)
	} else {
		log.Printf("[DATABASE] User %d saved/updated", id)
	}
}

// --- MARKET DATA LOGIC ---

func getPrice(symbol string, apiKey string) (float64, error) {
	log.Printf("[API] Fetching price for %s...", symbol)
	apiUrl := fmt.Sprintf("https://api.twelvedata.com/price?symbol=%s&apikey=%s", symbol, apiKey)
	resp, err := http.Get(apiUrl)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var result PriceResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Code == 429 || strings.Contains(strings.ToLower(result.Message), "credits") {
		log.Printf("[API ERROR] Rate limit or credits exhausted for %s", symbol)
		return 0, fmt.Errorf("API_LIMIT_EXCEEDED")
	}
	p, _ := strconv.ParseFloat(result.Price, 64)
	log.Printf("[API] %s Price: %.2f", symbol, p)
	return p, nil
}

func getCachedUsdVnd(apiKey string) (float64, error) {
	if time.Since(lastCacheUpdate) < cacheDuration && cachedUsdVnd > 0 {
		log.Println("[CACHE] Using cached USD/VND rate")
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

func translateToVietnamese(text string) string {
	scriptURL := os.Getenv("GOOGLE_SCRIPT_URL")
	apiURL := fmt.Sprintf("%s?text=%s&source=en&target=vi", scriptURL, url.QueryEscape(text))
	resp, _ := http.Get(apiURL)
	if resp == nil {
		return text
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	return string(body)
}

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

func getMarketUpdate() string {
	log.Println("[SYSTEM] Generating market update report...")
	apiKey := os.Getenv("TWELVE_DATA_API_KEY")
	now := time.Now()
	dateStr := now.Format("02/01/2006 15:04:05")

	pGold, _ := getPrice("XAU/USD", apiKey)
	pEUR, _ := getPrice("EUR/USD", apiKey)
	pBTC, _ := getPrice("BTC/USD", apiKey)
	usdToVnd, _ := getCachedUsdVnd(apiKey)

	if pGold == 0 {
		return fmt.Sprintf("📅 **Bản tin [%s]**\n⚠️ API credits exhausted.", dateStr)
	}

	log.Println("[RSS] Fetching news from Investing.com...")
	fp := gofeed.NewParser()
	feed, _ := fp.ParseURL("https://www.investing.com/rss/news_25.rss")
	newsList := ""
	if feed != nil {
		log.Printf("[RSS] Successfully parsed %d items", len(feed.Items))
		for i, item := range feed.Items {
			if i >= 7 {
				break
			}
			viTitle := translateToVietnamese(item.Title)
			newsList += fmt.Sprintf("🔹 **%s**\n🔗 [Xem chi tiết](%s)\n\n", viTitle, item.Link)
		}
	}

	log.Println("[SYSTEM] Market update report generated successfully")
	return fmt.Sprintf(
		"📅 **Nhịp Đập Thị Trường [%s]**\n"+
			"━━━━━━━━━━━━━━━━━━\n\n"+
			"🔴 **TIN TỨC QUAN TRỌNG:**\n\n%s"+
			"📈 **XU HƯỚNG THỊ TRƯỜNG:**\n"+
			"• Tỷ giá USD/VND: 1$ ≈ **%s VNĐ**\n"+
			"• Vàng (XAUUSD): $%.2f ≈ **%s VNĐ**\n"+
			"• EURUSD: %.4f ≈ **%s VNĐ**\n"+
			"• Bitcoin: $%.2f ≈ **%s VNĐ**\n\n"+
			"━━━━━━━━━━━━━━━━━━\n"+
			"💡 *Gõ `/update` để cập nhật dữ liệu mới nhất.*",
		dateStr, newsList, formatVnd(usdToVnd), pGold, formatVnd(pGold*usdToVnd),
		pEUR, formatVnd(pEUR*usdToVnd), pBTC, formatVnd(pBTC*usdToVnd),
	)
}

// --- HANDLERS ---

func Handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	initDatabase()
	token := os.Getenv("TELEGRAM_TOKEN")
	b, _ := tele.NewBot(tele.Settings{
		Token:       token,
		Synchronous: true,
	})

	if request.HTTPMethod == "" {
		log.Println("[LAMBDA] Cron trigger received")
		users := loadUsers()
		msg := getMarketUpdate()
		for id := range users {
			b.Send(&tele.Chat{ID: id}, msg, &tele.SendOptions{ParseMode: tele.ModeMarkdown, DisableWebPagePreview: true})
		}
		return events.APIGatewayProxyResponse{StatusCode: 200, Body: "Broadcast sent"}, nil
	}

	var update tele.Update
	if err := json.Unmarshal([]byte(request.Body), &update); err != nil {
		return events.APIGatewayProxyResponse{StatusCode: 400}, nil
	}

	if update.Message != nil {
		m := update.Message
		log.Printf("[LAMBDA] Incoming message from %d: %s", m.Chat.ID, m.Text)
		switch m.Text {
		case "/start":
			saveUser(m.Chat.ID)
			b.Send(m.Chat, "Chào mừng Trader! Bạn đã đăng ký nhận bản tin 8h sáng.")
		case "/update":
			b.Send(m.Chat, getMarketUpdate(), &tele.SendOptions{ParseMode: tele.ModeMarkdown, DisableWebPagePreview: true})
		}
	}

	return events.APIGatewayProxyResponse{StatusCode: 200, Body: "OK"}, nil
}

func main() {
	godotenv.Load()

	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		lambda.Start(Handler)
	} else {
		log.Println("🚀 Starting Bot in LOCAL LONG-POLLING mode...")
		initDatabase()
		
		token := os.Getenv("TELEGRAM_TOKEN")
		b, err := tele.NewBot(tele.Settings{
			Token:  token,
			Poller: &tele.LongPoller{Timeout: 10 * time.Second},
		})
		if err != nil {
			log.Fatal(err)
		}

		// --- LOCAL CRONJOB (Every 1 Minute) ---
		c := cron.New()
		c.AddFunc("@every 1m", func() {
			log.Println("[LOCAL CRON] Running 1-minute test broadcast...")
			users := loadUsers()
			if len(users) == 0 {
				log.Println("[LOCAL CRON] No users found in database.")
				return
			}
			msg := getMarketUpdate()
			for id := range users {
				log.Printf("[LOCAL CRON] Sending update to %d", id)
				b.Send(&tele.Chat{ID: id}, msg, &tele.SendOptions{ParseMode: tele.ModeMarkdown, DisableWebPagePreview: true})
			}
		})
		c.Start()
		log.Println("[LOCAL] 1-minute Cronjob started")

		// --- LOCAL HANDLERS ---
		b.Handle("/start", func(c tele.Context) error {
			log.Printf("[LOCAL] User %d triggered /start", c.Chat().ID)
			saveUser(c.Chat().ID)
			return c.Send("Chào mừng Trader! (Local Mode)")
		})

		b.Handle("/update", func(c tele.Context) error {
			log.Printf("[LOCAL] User %d requested /update", c.Chat().ID)
			update := getMarketUpdate()
			return c.Send(update, &tele.SendOptions{ParseMode: tele.ModeMarkdown, DisableWebPagePreview: true})
		})

		log.Println("[SYSTEM] Bot is listening. Press Ctrl+C to stop.")
		b.Start()
	}
}