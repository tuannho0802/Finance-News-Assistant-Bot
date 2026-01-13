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
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/joho/godotenv"
	"github.com/mmcdole/gofeed"
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

// PriceResponse updated to include percent_change from API
type PriceResponse struct {
	Price         string `json:"price"`
	PercentChange string `json:"percent_change"`
	Code          int    `json:"code"`
	Message       string `json:"message"`
}

// MarketData struct to hold both price and formatted change string
type MarketData struct {
	Price  float64
	Change string
}

// --- CONSTANTS ---

const helpMessage = `📖 *HƯỚNG DẪN SỬ DỤNG BOT*

Dưới đây là danh sách các lệnh bạn có thể sử dụng:

🚀 *Khởi đầu:*
/start - Đăng ký nhận bản tin thị trường tự động hàng ngày.

📊 *Tra cứu:*
/update - Xem ngay báo cáo thị trường mới nhất (Vàng, BTC, Ngoại tệ & Tin tức).
/help - Xem danh sách lệnh và hướng dẫn này.

❌ *Ngừng nhận tin:*
/quit hoặc /cancel - Hủy đăng ký và xóa dữ liệu của bạn khỏi hệ thống nhận tin tự động.

💡 *Mẹo:* Bạn có thể nhấn nút "Cập nhật giá mới" bên dưới mỗi bản tin để làm mới dữ liệu nhanh chóng.`

// --- DATABASE LOGIC ---

// initDatabase initializes connection to MongoDB Atlas
func initDatabase() {
	uri := os.Getenv("MONGODB_URI")
	// Set a timeout for connection to prevent hanging during cold starts
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		log.Printf("[DATABASE ERROR] Connection failed: %v", err)
		return
	}
	userCollection = client.Database("market_bot").Collection("users")
	log.Println("[DATABASE] Connected to MongoDB Atlas")
}

// loadUsers retrieves all subscribed chat IDs
func loadUsers() map[int64]bool {
	users := make(map[int64]bool)
	if userCollection == nil {
		log.Println("[DATABASE ERROR] Collection is nil")
		return users
	}
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

// saveUser adds or updates a user chat ID in the database
func saveUser(id int64) {
	if userCollection == nil {
		log.Println("[DATABASE ERROR] Cannot save, collection is nil")
		return
	}
	filter := bson.M{"chat_id": id}
	update := bson.M{"$set": bson.M{"chat_id": id, "updated_at": time.Now()}}
	_, err := userCollection.UpdateOne(context.TODO(), filter, update, options.Update().SetUpsert(true))
	if err != nil {
		log.Printf("[DATABASE ERROR] Failed to save user %d: %v", id, err)
	} else {
		log.Printf("[DATABASE] User %d saved/updated", id)
	}
}

// removeUser deletes a user from MongoDB by chat ID
func removeUser(id int64) bool {
	if userCollection == nil {
		log.Println("[DATABASE ERROR] Cannot delete, collection is nil")
		return false
	}
	filter := bson.M{"chat_id": id}
	result, err := userCollection.DeleteOne(context.TODO(), filter)
	if err != nil {
		log.Printf("[DATABASE ERROR] Failed to remove user %d: %v", id, err)
		return false
	}
	return result.DeletedCount > 0
}

// --- MARKET DATA LOGIC ---

// getMarketData fetches financial data from Twelve Data API
func getMarketData(symbol string, apiKey string) MarketData {
	log.Printf("[API] Fetching quote for %s...", symbol)
	apiUrl := fmt.Sprintf("https://api.twelvedata.com/quote?symbol=%s&apikey=%s", symbol, apiKey)
	// Explicit client with timeout to prevent Lambda from hanging
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(apiUrl)
	if err != nil {
		log.Printf("[API ERROR] Request failed for %s: %v", symbol, err)
		return MarketData{Price: 0, Change: "0.00%"}
	}
	defer resp.Body.Close()

	var result struct {
		Close         string `json:"close"`
		PercentChange string `json:"percent_change"`
		Message       string `json:"message"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if result.Message != "" {
		log.Printf("[API ERROR] Message from TwelveData for %s: %s", symbol, result.Message)
		return MarketData{Price: 0, Change: "N/A"}
	}

	p, _ := strconv.ParseFloat(result.Close, 64)
	c, _ := strconv.ParseFloat(result.PercentChange, 64)
	// Format change string with market trend indicators
	changeStr := fmt.Sprintf("%.2f%%", c)
	if c > 0 {
		changeStr = "📈 +" + changeStr
	} else if c < 0 {
		changeStr = "📉 " + changeStr
	}

	return MarketData{Price: p, Change: changeStr}
}

// getCachedUsdVnd manages caching for USD/VND rates to save API credits
func getCachedUsdVnd(apiKey string) (float64, error) {
	if time.Since(lastCacheUpdate) < cacheDuration && cachedUsdVnd > 0 {
		log.Println("[CACHE] Using cached USD/VND rate")
		return cachedUsdVnd, nil
	}
	data := getMarketData("USD/VND", apiKey)
	if data.Price == 0 {
		return 25000, fmt.Errorf("API_ERROR")
	}
	cachedUsdVnd = data.Price
	lastCacheUpdate = time.Now()
	return cachedUsdVnd, nil
}

// translateToVietnamese uses Google Apps Script to translate news headlines
func translateToVietnamese(text string) string {
	scriptURL := os.Getenv("GOOGLE_SCRIPT_URL")
	if scriptURL == "" {
		return text
	}
	apiURL := fmt.Sprintf("%s?text=%s&source=en&target=vi", scriptURL, url.QueryEscape(text))
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil || resp == nil {
		return text
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	return string(body)
}

// formatVnd adds thousands separators to currency values
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

// getMarketUpdate aggregates all market news and data into a single message
func getMarketUpdate() (string, *tele.ReplyMarkup) {
	log.Println("[SYSTEM] Generating market update report...")
	apiKey := os.Getenv("TWELVE_DATA_API_KEY")
	now := time.Now()
	dateStr := now.Format("02/01/2006 15:04:05")

	gold := getMarketData("XAU/USD", apiKey)
	eur := getMarketData("EUR/USD", apiKey)
	btc := getMarketData("BTC/USD", apiKey)
	usdToVnd, _ := getCachedUsdVnd(apiKey)

	if gold.Price == 0 {
		return fmt.Sprintf("📅 **Bản tin [%s]**\n⚠️ API credits exhausted or market closed.", dateStr), nil
	}

	log.Println("[RSS] Fetching news from Investing.com...")
	fp := gofeed.NewParser()
	feed, _ := fp.ParseURL("https://www.investing.com/rss/news_25.rss")
	newsList := ""
	if feed != nil {
		for i, item := range feed.Items {
			if i >= 8 {
				break
			}
			viTitle := translateToVietnamese(item.Title)
			newsList += fmt.Sprintf("🔹 **%s**\n🔗 [Xem chi tiết](%s)\n\n", viTitle, item.Link)
		}
	}

	report := fmt.Sprintf(
		"💰 **NHỊP ĐẬP THỊ TRƯỜNG**\n📅 *Cập nhật: %s*\n"+
			"━━━━━━━━━━━━━━━━━━\n\n"+
			"🔴 **TIN TỨC QUAN TRỌNG:**\n\n%s"+
			"📈 **XU HƯỚNG THỊ TRƯỜNG:**\n"+
			"• 💵 Tỷ giá USD/VND: 1$ ≈ **%s VNĐ**\n"+
			"• 🟡 Vàng (XAUUSD): `$%.2f` (%s)\n"+
			"• 🇪🇺 EURUSD: `%.4f` (%s)\n"+
			"• ₿ Bitcoin: `$%.2f` (%s)\n\n"+
			"━━━━━━━━━━━━━━━━━━\n"+
			"💡 *Nhấn nút bên dưới để cập nhật nhanh*",
		dateStr, newsList, formatVnd(usdToVnd),
		gold.Price, gold.Change,
		eur.Price, eur.Change,
		btc.Price, btc.Change,
	)

	menu := &tele.ReplyMarkup{}
	btnUpdate := menu.Data("🔄 Cập nhật giá mới", "btn_update_price")
	menu.Inline(menu.Row(btnUpdate))

	return report, menu
}

// --- HANDLERS (AWS LAMBDA) ---

// Handler processes AWS Lambda requests (Function URL triggers)
func Handler(ctx context.Context, request events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	initDatabase()
	token := os.Getenv("TELEGRAM_TOKEN")
	// Initialize bot in synchronous mode for Lambda environment
	b, err := tele.NewBot(tele.Settings{
		Token:       token,
		Synchronous: true,
	})
	if err != nil {
		log.Printf("[ERROR] Bot initialization failed: %v", err)
		return events.LambdaFunctionURLResponse{StatusCode: 500}, nil
	}
	// --- CRON TRIGGER / DIRECT CALL ---
	// EventBridge or direct URL calls without body are treated as broadcast triggers
	if request.Body == "" {
		log.Println("[LAMBDA] Empty body trigger detected")
		users := loadUsers()
		msg, menu := getMarketUpdate()
		for id := range users {
			b.Send(&tele.Chat{ID: id}, msg, &tele.SendOptions{
				ParseMode:             tele.ModeMarkdown,
				ReplyMarkup:           menu,
				DisableWebPagePreview: true,
			})
		}
		return events.LambdaFunctionURLResponse{StatusCode: 200, Body: "Broadcast complete"}, nil
	}

	var update tele.Update
	if err := json.Unmarshal([]byte(request.Body), &update); err != nil {
		log.Printf("[ERROR] Failed to parse Telegram update: %v", err)
		return events.LambdaFunctionURLResponse{StatusCode: 200, Body: "Malformed request"}, nil
	}

	if update.Callback != nil {
		log.Printf("[LAMBDA] Callback interaction: %s", update.Callback.Data)
		b.Edit(update.Callback.Message, update.Callback.Message.Text+"\n\n⌛ *Đang cập nhật dữ liệu...*", &tele.SendOptions{
			ParseMode:   tele.ModeMarkdown,
			ReplyMarkup: update.Callback.Message.ReplyMarkup,
		})

		msg, menu := getMarketUpdate()
		b.Edit(update.Callback.Message, msg+"\n\n✅ *Cập nhật thành công!*", &tele.SendOptions{
			ParseMode:             tele.ModeMarkdown,
			ReplyMarkup:           menu,
			DisableWebPagePreview: true,
		})
		b.Respond(update.Callback, &tele.CallbackResponse{})
		return events.LambdaFunctionURLResponse{StatusCode: 200}, nil
	}
	// Handle Standard Messages
	if update.Message != nil {
		m := update.Message
		log.Printf("[LAMBDA] Message from %d: %s", m.Chat.ID, m.Text)
		switch m.Text {
		case "/start":
			saveUser(m.Chat.ID)
			b.Send(m.Chat, "Chào mừng Trader! Bạn đã đăng ký nhận bản tin tự động hàng ngày. Gõ /help để xem hướng dẫn.")
		case "/help":
			b.Send(m.Chat, helpMessage, &tele.SendOptions{ParseMode: tele.ModeMarkdown})
		case "/update":
			tmpMsg, _ := b.Send(m.Chat, "⌛ *Đang lấy dữ liệu thị trường mới nhất...*", &tele.SendOptions{ParseMode: tele.ModeMarkdown})
			msg, menu := getMarketUpdate()
			b.Edit(tmpMsg, msg, &tele.SendOptions{
				ParseMode:             tele.ModeMarkdown,
				ReplyMarkup:           menu,
				DisableWebPagePreview: true,
			})
		case "/quit", "/cancel":
			if removeUser(m.Chat.ID) {
				b.Send(m.Chat, "❌ Bạn đã hủy đăng ký nhận bản tin thành công. Hẹn gặp lại!")
			} else {
				b.Send(m.Chat, "ℹ️ Bạn hiện chưa đăng ký nhận bản tin hoặc đã hủy trước đó.")
			}
		default:
			// Fallback message for unrecognized commands
			b.Send(m.Chat, "🤖 Lệnh không hợp lệ. Vui lòng dùng /help để xem danh sách các lệnh hỗ trợ.")
		}
	}

	return events.LambdaFunctionURLResponse{StatusCode: 200, Body: "Processed"}, nil
}

// --- MAIN (LOCAL & PROD) ---

func main() {
	godotenv.Load()

	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		// Execution environment is AWS Lambda
		lambda.Start(Handler)
	} else {
		// Execution environment is Local Machine
		log.Println("🚀 Starting Bot in LOCAL mode...")
		initDatabase()

		token := os.Getenv("TELEGRAM_TOKEN")
		b, err := tele.NewBot(tele.Settings{
			Token:  token,
			Poller: &tele.LongPoller{Timeout: 10 * time.Second},
		})
		if err != nil {
			log.Fatal(err)
		}

		b.Handle("/start", func(c tele.Context) error {
			saveUser(c.Chat().ID)
			return c.Send("🛠 Chế độ thử nghiệm đã sẵn sàng. Bạn đã được đăng ký. Gõ /help để xem hướng dẫn.")
		})

		b.Handle("/help", func(c tele.Context) error {
			return c.Send(helpMessage, &tele.SendOptions{ParseMode: tele.ModeMarkdown})
		})

		b.Handle("/update", func(c tele.Context) error {
			tmpMsg, _ := b.Send(c.Chat(), "⌛ *Đang cập nhật dữ liệu...*", &tele.SendOptions{ParseMode: tele.ModeMarkdown})
			msg, menu := getMarketUpdate()
			_, err = b.Edit(tmpMsg, msg, &tele.SendOptions{
				ParseMode:             tele.ModeMarkdown,
				ReplyMarkup:           menu,
				DisableWebPagePreview: true,
			})
			return err
		})

		b.Handle("/quit", func(c tele.Context) error {
			if removeUser(c.Chat().ID) {
				return c.Send("❌ Đã hủy đăng ký nhận tin.")
			}
			return c.Send("ℹ️ Bạn chưa đăng ký.")
		})

		b.Handle("/cancel", func(c tele.Context) error {
			if removeUser(c.Chat().ID) {
				return c.Send("❌ Đã hủy đăng ký nhận tin.")
			}
			return c.Send("ℹ️ Bạn chưa đăng ký.")
		})

		// Catch-all handler for text that doesn't match specific commands
		b.Handle(tele.OnText, func(c tele.Context) error {
			return c.Send("🤖 Lệnh không hợp lệ. Vui lòng dùng /help để xem danh sách các lệnh hỗ trợ.")
		})

		b.Handle("\fbtn_update_price", func(c tele.Context) error {
			c.Respond(&tele.CallbackResponse{Text: "🔄 Đang lấy dữ liệu mới..."})
			msg, menu := getMarketUpdate()
			return c.Edit(msg+"\n\n✅ *Cập nhật thành công!*", &tele.SendOptions{
				ParseMode:             tele.ModeMarkdown,
				ReplyMarkup:           menu,
				DisableWebPagePreview: true,
			})
		})

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		go b.Start()
		<-stop
		b.Stop()
	}
}
