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

// Modified to use /quote endpoint for both price and percentage change
func getMarketData(symbol string, apiKey string) MarketData {
	log.Printf("[API] Fetching quote for %s...", symbol)
	apiUrl := fmt.Sprintf("https://api.twelvedata.com/quote?symbol=%s&apikey=%s", symbol, apiKey)
	resp, err := http.Get(apiUrl)
	if err != nil {
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
		log.Printf("[API ERROR] %s: %s", symbol, result.Message)
		return MarketData{Price: 0, Change: "N/A"}
	}

	p, _ := strconv.ParseFloat(result.Close, 64)
	c, _ := strconv.ParseFloat(result.PercentChange, 64)

	// Format change string with trend icons
	changeStr := fmt.Sprintf("%.2f%%", c)
	if c > 0 {
		changeStr = "📈 +" + changeStr
	} else if c < 0 {
		changeStr = "📉 " + changeStr
	}

	return MarketData{Price: p, Change: changeStr}
}

func getCachedUsdVnd(apiKey string) (float64, error) {
	if time.Since(lastCacheUpdate) < cacheDuration && cachedUsdVnd > 0 {
		log.Println("[CACHE] Using cached USD/VND rate")
		return cachedUsdVnd, nil
	}
	// Fetching current rate from API
	data := getMarketData("USD/VND", apiKey)
	if data.Price == 0 {
		return 25000, fmt.Errorf("API_ERROR")
	}
	cachedUsdVnd = data.Price
	lastCacheUpdate = time.Now()
	return cachedUsdVnd, nil
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

// Modified to return message string and Inline Keyboard markup
func getMarketUpdate() (string, *tele.ReplyMarkup) {
	log.Println("[SYSTEM] Generating market update report...")
	apiKey := os.Getenv("TWELVE_DATA_API_KEY")
	now := time.Now()
	dateStr := now.Format("02/01/2006 15:04:05")

	// Fetch financial data with daily change
	gold := getMarketData("XAU/USD", apiKey)
	eur := getMarketData("EUR/USD", apiKey)
	btc := getMarketData("BTC/USD", apiKey)
	usdToVnd, _ := getCachedUsdVnd(apiKey)

	if gold.Price == 0 {
		return fmt.Sprintf("📅 **Bản tin [%s]**\n⚠️ API credits exhausted.", dateStr), nil
	}

	log.Println("[RSS] Fetching news from Investing.com...")
	fp := gofeed.NewParser()
	feed, _ := fp.ParseURL("https://www.investing.com/rss/news_25.rss")
	newsList := ""
	if feed != nil {
		log.Printf("[RSS] Successfully parsed %d items", len(feed.Items))
		for i, item := range feed.Items {
			if i >= 8 {
				break
			}
			viTitle := translateToVietnamese(item.Title)
			newsList += fmt.Sprintf("🔹 **%s**\n🔗 [Xem chi tiết](%s)\n\n", viTitle, item.Link)
		}
	}

	// Build report string with new UI format
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

	// Create Inline Button for quick update
	menu := &tele.ReplyMarkup{}
	btnUpdate := menu.Data("🔄 Cập nhật giá mới", "btn_update_price")
	menu.Inline(menu.Row(btnUpdate))

	log.Println("[SYSTEM] Market update report generated successfully")
	return report, menu
}

// --- HANDLERS (AWS LAMBDA) ---

// Updated to use LambdaFunctionURLRequest for compatibility with AWS Lambda Function URL
func Handler(ctx context.Context, request events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	initDatabase()
	token := os.Getenv("TELEGRAM_TOKEN")
	b, _ := tele.NewBot(tele.Settings{
		Token:       token,
		Synchronous: true,
	})

	// --- CRON TRIGGER ---
	// Updated condition to check empty body which is common for EventBridge/Direct URL calls
	if request.Body == "" {
		log.Println("[LAMBDA] Cron trigger received")
		users := loadUsers()
		msg, menu := getMarketUpdate()
		for id := range users {
			b.Send(&tele.Chat{ID: id}, msg, &tele.SendOptions{
				ParseMode:             tele.ModeMarkdown,
				ReplyMarkup:           menu,
				DisableWebPagePreview: true,
			})
		}
		return events.LambdaFunctionURLResponse{StatusCode: 200, Body: "Broadcast sent"}, nil
	}

	var update tele.Update
	if err := json.Unmarshal([]byte(request.Body), &update); err != nil {
		// Return 200 even on error to prevent Telegram from retrying indefinitely
		return events.LambdaFunctionURLResponse{StatusCode: 200}, nil
	}

	// Handle Inline Button callback for Lambda with Double Edit logic
	if update.Callback != nil {
		log.Printf("[LAMBDA] Inline button clicked: %s", update.Callback.Data)

		// Provide status update to user
		b.Edit(update.Callback.Message, update.Callback.Message.Text+"\n\n⌛ *Đang cập nhật dữ liệu...*", &tele.SendOptions{
			ParseMode:   tele.ModeMarkdown,
			ReplyMarkup: update.Callback.Message.ReplyMarkup,
		})

		msg, menu := getMarketUpdate()

		// Send final report
		b.Edit(update.Callback.Message, msg+"\n\n✅ *Cập nhật thành công!*", &tele.SendOptions{
			ParseMode:             tele.ModeMarkdown,
			ReplyMarkup:           menu,
			DisableWebPagePreview: true,
		})
		b.Respond(update.Callback, &tele.CallbackResponse{})
		return events.LambdaFunctionURLResponse{StatusCode: 200}, nil
	}

	if update.Message != nil {
		m := update.Message
		log.Printf("[LAMBDA] Incoming message from %d: %s", m.Chat.ID, m.Text)
		switch m.Text {
		case "/start":
			saveUser(m.Chat.ID)
			b.Send(m.Chat, "Chào mừng Trader! Bạn đã đăng ký nhận bản tin tự động.")
		case "/update":
			// Send immediate feedback before API call
			tmpMsg, err := b.Send(m.Chat, "⌛ *Đang lấy dữ liệu thị trường mới nhất...*", &tele.SendOptions{ParseMode: tele.ModeMarkdown})
			if err != nil {
				log.Printf("[ERROR] Failed to send temp message: %v", err)
			}

			msg, menu := getMarketUpdate()

			// Update initial message with actual data
			b.Edit(tmpMsg, msg, &tele.SendOptions{
				ParseMode:             tele.ModeMarkdown,
				ReplyMarkup:           menu,
				DisableWebPagePreview: true,
			})
		default:
			b.Send(m.Chat, "🤖 Vui lòng sử dụng /update để cập nhật thị trường mới nhất.")
		}
	}

	return events.LambdaFunctionURLResponse{StatusCode: 200, Body: "OK"}, nil
}

// --- MAIN (LOCAL MODE) ---

func main() {
	godotenv.Load()

	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		// --- PRODUCTION MODE (AWS LAMBDA) ---
		lambda.Start(Handler)
	} else {
		// --- DEVELOPMENT MODE (LOCAL) ---
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

		// Commented out to prevent accidental webhook removal on production bot during testing
		// b.RemoveWebhook()

		// --- REGISTER HANDLERS ---
		b.Handle("/start", func(c tele.Context) error {
			saveUser(c.Chat().ID)
			return c.Send("🛠 Chế độ thử nghiệm (Local Mode) đã sẵn sàng.")
		})

		b.Handle("/update", func(c tele.Context) error {
			log.Printf("[LOCAL] Requesting market update...")

			// Provide immediate feedback to the user
			tmpMsg, err := b.Send(c.Chat(), "⌛ *Đang kết nối hệ thống dữ liệu...*", &tele.SendOptions{ParseMode: tele.ModeMarkdown})
			if err != nil {
				log.Printf("[LOCAL ERROR] Could not send placeholder: %v", err)
			}

			msg, menu := getMarketUpdate()

			// Replace placeholder with live data
			_, err = b.Edit(tmpMsg, msg, &tele.SendOptions{
				ParseMode:             tele.ModeMarkdown,
				ReplyMarkup:           menu,
				DisableWebPagePreview: true,
			})
			return err
		})

		// --- LOCAL CALLBACK HANDLERS ---
		b.Handle("\fbtn_update_price", func(c tele.Context) error {
			log.Printf("[LOCAL] Callback 'btn_update_price' received.")

			// Acknowledge callback immediately
			c.Respond(&tele.CallbackResponse{Text: "🔄 Đang lấy dữ liệu mới..."})

			// Visual feedback for long-running operation
			oldText := c.Message().Text
			loadingText := oldText + "\n\n⌛ *Đang kết nối API và cập nhật dữ liệu...*"

			c.Edit(loadingText, &tele.SendOptions{
				ParseMode:             tele.ModeMarkdown,
				ReplyMarkup:           c.Message().ReplyMarkup,
				DisableWebPagePreview: true,
			})

			msg, menu := getMarketUpdate()

			// Final render with fresh data
			finalMsg := msg + "\n\n✅ *Cập nhật thành công!*"

			return c.Edit(finalMsg, &tele.SendOptions{
				ParseMode:             tele.ModeMarkdown,
				ReplyMarkup:           menu,
				DisableWebPagePreview: true,
			})
		})

		b.Handle(tele.OnText, func(c tele.Context) error {
			return c.Send("🤖 Bot đang chạy Local. Chỉ nhận lệnh /update.")
		})

		// --- GRACEFUL SHUTDOWN LOGIC ---
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

		go func() {
			log.Println("[SYSTEM] Bot is listening. Press Ctrl+C to stop.")
			b.Start()
		}()

		<-stop

		log.Println("\n[SHUTDOWN] Gracefully shutting down...")
		b.Stop()
		log.Println("[SHUTDOWN] Bot stopped. Exit successful.")
	}
}