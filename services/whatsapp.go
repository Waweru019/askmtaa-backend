package services

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	"main/models"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver
)

type VendorSession struct {
	State                  string
	User                   models.User
	Vendor                 models.Vendor
	LastSearchResults      []models.Listing // Stores current search/category results
	LastSearchHeader       string           // Stores header title
	CurrentPage            int              // Current page number (1, 2, 3...)
	FailedPasswordAttempts int              // Tracks invalid login attempts
}

var (
	userSessions          = make(map[string]*VendorSession)
	sessionMutex          sync.RWMutex
	GlobalWhatsAppService *WhatsAppService // Shared instance for queue dispatches

	// Shared HTTP Client with connection pooling to eliminate TLS handshake latency
	httpClient = &http.Client{
		Timeout: 12 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     90 * time.Second,
		},
	}
)

const WebDashboardURL = "https://askmtaa.vercel.app"
const PageSize = 5 // Number of listings per WhatsApp page
const MaxFailedAttempts = 3

// Helper: Generates all common Kenyan phone number formats (+254..., 254..., 07..., 01...)
func getPhoneVariants(rawJIDUser string) []string {
	re := regexp.MustCompile(`\D`)
	clean := re.ReplaceAllString(rawJIDUser, "")

	variants := []string{clean}

	if strings.HasPrefix(clean, "254") && len(clean) == 12 {
		local := "0" + clean[3:]
		variants = append(variants, local, "+"+clean, "+"+local)
	} else if strings.HasPrefix(clean, "0") && len(clean) == 10 {
		intl := "254" + clean[1:]
		variants = append(variants, intl, "+"+intl, "+"+clean)
	}

	return variants
}

type WhatsAppService struct {
	Client *whatsmeow.Client
	DB     *gorm.DB
}

// InitWhatsApp initializes the whatsmeow client and connects to WhatsApp
func InitWhatsApp(db *gorm.DB) (*WhatsAppService, error) {
	ctx := context.Background()

	dbLog := waLog.Stdout("Database", "WARN", true)
	clientLog := waLog.Stdout("Client", "INFO", true)

	container, err := sqlstore.New(ctx, "sqlite", "file:whatsapp_session.db?_foreign_keys=on", dbLog)
	if err != nil {
		return nil, fmt.Errorf("failed to open session db: %w", err)
	}

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get device store: %w", err)
	}

	client := whatsmeow.NewClient(deviceStore, clientLog)
	service := &WhatsAppService{
		Client: client,
		DB:     db,
	}

	client.AddEventHandler(service.handleIncomingMessage)

	if client.Store.ID == nil {
		fmt.Println("🔄 Requesting WhatsApp QR Code from server...")
		qrChan, _ := client.GetQRChannel(ctx)

		if err := client.Connect(); err != nil {
			return nil, fmt.Errorf("failed to connect WhatsApp client: %w", err)
		}

		for evt := range qrChan {
			if evt.Event == "code" {
				fmt.Println("\n=======================================================")
				fmt.Println("SCAN THIS QR CODE IN WHATSAPP (Settings -> Linked Devices):")
				fmt.Println("=======================================================")
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			} else {
				fmt.Println("📱 WhatsApp Event:", evt.Event)
			}
		}
	} else {
		fmt.Println("🔄 Reconnecting existing WhatsApp session...")
		if err := client.Connect(); err != nil {
			return nil, fmt.Errorf("failed to reconnect WhatsApp: %w", err)
		}
		fmt.Println("📱 WhatsApp client connected successfully!")
	}

	// Assign global instance for the background queue worker
	GlobalWhatsAppService = service

	return service, nil
}

// SendTextMessagePublic exposes sending capability to the queue worker
func (s *WhatsAppService) SendTextMessagePublic(phone string, text string) error {
	toJID, ok := parsePhoneToJID(phone)
	if !ok {
		return fmt.Errorf("invalid phone number format: %s", phone)
	}
	return s.sendTextMessage(toJID, text)
}

// Simulates real human interaction (Read receipts + typing presence + random jitter) to prevent account bans
func (s *WhatsAppService) simulateHumanPresence(toJID types.JID, messageID types.MessageID) {
	ctx := context.Background()

	// 1. Send Read Receipt (Blue Ticks)
	if messageID != "" {
		_ = s.Client.MarkRead(ctx, []types.MessageID{messageID}, time.Now(), toJID, toJID)
	}

	// 2. Emit "typing..." status
	_ = s.Client.SendChatPresence(ctx, toJID, types.ChatPresenceComposing, types.ChatPresenceMediaText)

	// 3. Jittered random human delay (1.8s to 3.8s)
	randomMillis := 1800 + rand.Intn(2000)
	time.Sleep(time.Duration(randomMillis) * time.Millisecond)

	// 4. Reset presence state before dispatching
	_ = s.Client.SendChatPresence(ctx, toJID, types.ChatPresencePaused, types.ChatPresenceMediaText)
}

// handleIncomingMessage processes incoming WhatsApp messages including text and media types
func (s *WhatsAppService) handleIncomingMessage(evt interface{}) {
	v, ok := evt.(*events.Message)
	if !ok || v.Info.IsFromMe {
		return
	}

	senderJID := v.Info.Sender
	msgID := v.Info.ID
	msg := v.Message
	if msg == nil {
		return
	}

	var incomingText string

	// 1. Extract Text from Standard Conversation or Extended Text Message
	if msg.GetConversation() != "" {
		incomingText = msg.GetConversation()
	} else if msg.GetExtendedTextMessage() != nil {
		incomingText = msg.GetExtendedTextMessage().GetText()
	}

	// 2. Handle Image Messages (Check if user added a caption)
	if incomingText == "" && msg.GetImageMessage() != nil {
		caption := msg.GetImageMessage().GetCaption()
		if strings.TrimSpace(caption) != "" {
			incomingText = caption
		} else {
			fmt.Printf("[WhatsApp] Received image without caption from %s\n", senderJID.User)
			s.simulateHumanPresence(senderJID, msgID)
			reply := "📷 *Image Received!*\n\n"
			reply += "If you're searching for a service or product pictured here, please type a short description as a text message (e.g., *shoe repair*, *plumber*, *salon*)."
			s.sendTextMessage(senderJID, reply)
			return
		}
	}

	// 3. Handle GPS Location Pins (LocationMessage or LiveLocationMessage)
	if incomingText == "" && (msg.GetLocationMessage() != nil || msg.GetLiveLocationMessage() != nil) {
		fmt.Printf("[WhatsApp] Received GPS Location Pin from %s\n", senderJID.User)

		var gpsDetails string
		loc := msg.GetLocationMessage()
		if loc != nil && (loc.GetDegreesLatitude() != 0 || loc.GetDegreesLongitude() != 0) {
			gpsDetails = fmt.Sprintf(" (GPS: %.4f, %.4f)", loc.GetDegreesLatitude(), loc.GetDegreesLongitude())
		}

		s.simulateHumanPresence(senderJID, msgID)
		reply := fmt.Sprintf("📍 *Location Pin Received%s!*\n\n", gpsDetails)
		reply += "AskMtaa searches services by area name. Please type your neighborhood/town alongside the service you need:\n\n"
		reply += "👉 Example: *laundry in Embu* or *boda in Kasarani*\n"
		reply += "👉 Or reply *menu* to browse all categories."

		s.sendTextMessage(senderJID, reply)
		return
	}

	// 4. Handle Voice Notes & Audio Messages
	if incomingText == "" && msg.GetAudioMessage() != nil {
		fmt.Printf("[WhatsApp] Received Voice Note/Audio from %s\n", senderJID.User)
		s.simulateHumanPresence(senderJID, msgID)
		reply := "🎙️ *Voice Note Received!*\n\n"
		reply += "Our automated bot currently reads text messages. Please type what service or product you are looking for (e.g., *electrician*, *cleaning*), or reply *menu* to browse categories."
		s.sendTextMessage(senderJID, reply)
		return
	}

	// 5. Fallback for Unsupported Media (Documents, Videos, Stickers, Contacts)
	if strings.TrimSpace(incomingText) == "" {
		if msg.GetDocumentMessage() != nil || msg.GetVideoMessage() != nil || msg.GetStickerMessage() != nil || msg.GetContactMessage() != nil {
			fmt.Printf("[WhatsApp] Received unsupported media message from %s\n", senderJID.User)
			s.simulateHumanPresence(senderJID, msgID)
			reply := "📎 *Media Received!*\n\n"
			reply += "AskMtaa works best with text search. Please reply with the product or service you need, or reply *menu* for main categories."
			s.sendTextMessage(senderJID, reply)
			return
		}
		return
	}

	fmt.Printf("[WhatsApp] Message from %s: %s\n", senderJID.User, incomingText)

	// Route message and handle listings or general replies
	s.routeMessage(senderJID, msgID, incomingText)
}

// SendVendorListingCard sends a single unified contact card containing listing title, price, and vendor info
func (s *WhatsAppService) SendVendorListingCard(toJID types.JID, listingTitle string, price float64, vendorName string, phone string) error {
	clean := strings.TrimSpace(phone)
	clean = strings.ReplaceAll(clean, "+", "")
	clean = strings.ReplaceAll(clean, " ", "")

	if strings.HasPrefix(clean, "0") && len(clean) == 10 {
		clean = "254" + clean[1:]
	}

	displayName := fmt.Sprintf("%s - KES %.2f", listingTitle, price)
	if price <= 0 {
		displayName = listingTitle
	}

	if vendorName == "" {
		vendorName = "AskMtaa Seller"
	}

	vcard := fmt.Sprintf("BEGIN:VCARD\nVERSION:3.0\nN:;%s;;;\nFN:%s\nORG:Vendor: %s\nTEL;type=CELL;waid=%s:+%s\nEND:VCARD",
		displayName, displayName, vendorName, clean, clean)

	msg := &waE2E.Message{
		ContactMessage: &waE2E.ContactMessage{
			DisplayName: proto.String(displayName),
			Vcard:       proto.String(vcard),
		},
	}

	_, err := s.Client.SendMessage(context.Background(), toJID, msg)
	return err
}

// routeMessage handles category selection, sub-services fetching, and keyword search
func (s *WhatsAppService) routeMessage(toJID types.JID, messageID types.MessageID, rawMessage string) {
	trimmedRaw := strings.TrimSpace(rawMessage)
	clean := strings.ToLower(trimmedRaw)
	if clean == "" {
		return
	}

	// 🛡️ Human Presence Simulation (Mark read + typing status + jitter)
	s.simulateHumanPresence(toJID, messageID)

	senderKey := toJID.ToNonAD().User

	// 1. FETCH ALL ACTIVE CATEGORIES
	var categories []models.Category
	s.DB.Order("id asc").Find(&categories)

	sessionMutex.RLock()
	session, hasSession := userSessions[senderKey]
	sessionMutex.RUnlock()

	// -------------------------------------------------------------
	// 1. ACTIVE VENDOR SESSIONS (State Machine Guard)
	// -------------------------------------------------------------
	isVendorState := hasSession && session != nil && (
		session.State == "AWAITING_IDENTIFIER" ||
			session.State == "AWAITING_PASSWORD" ||
			session.State == "LOGGED_IN" ||
			strings.HasPrefix(session.State, "ADD_"))

	if isVendorState {
		switch session.State {

		// STEP 1: VERIFY PHONE OR EMAIL
		case "AWAITING_IDENTIFIER":
			identifier := strings.TrimSpace(clean)

			if identifier == "cancel" {
				sessionMutex.Lock()
				delete(userSessions, senderKey)
				sessionMutex.Unlock()
				s.sendTextMessage(toJID, "Cancelled vendor login. Reply *menu* for main menu.")
				return
			}

			phoneVariants := getPhoneVariants(identifier)
			var user models.User

			err := s.DB.Preload("Vendor").
				Where("LOWER(email) = ? OR phone_number IN ?", strings.ToLower(identifier), phoneVariants).
				First(&user).Error

			if err != nil || user.Vendor.ID == uuid.Nil {
				s.sendTextMessage(toJID, fmt.Sprintf("❌ No vendor account found for '%s'.\n\nPlease check your email/phone and try again, or reply *cancel* to exit.", identifier))
				return
			}

			sessionMutex.Lock()
			session.State = "AWAITING_PASSWORD"
			session.User = user
			session.Vendor = user.Vendor
			sessionMutex.Unlock()

			fmt.Printf("👤 [User Identified] Email: %s | Business: %s\n", user.Email, user.Vendor.BusinessName)
			s.sendTextMessage(toJID, fmt.Sprintf("👤 Account found for *%s*!\n\nPlease enter your password to log in:", user.Vendor.BusinessName))
			return

		// STEP 2: VERIFY PASSWORD WITH BRUTE-FORCE PROTECTION
		case "AWAITING_PASSWORD":
			passwordInput := trimmedRaw

			if strings.ToLower(passwordInput) == "cancel" {
				sessionMutex.Lock()
				delete(userSessions, senderKey)
				sessionMutex.Unlock()
				s.sendTextMessage(toJID, "Cancelled vendor login. Reply *menu* for main menu.")
				return
			}

			err := bcrypt.CompareHashAndPassword([]byte(session.User.PasswordHash), []byte(passwordInput))
			if err != nil {
				sessionMutex.Lock()
				session.FailedPasswordAttempts++
				currentAttempts := session.FailedPasswordAttempts
				attemptsLeft := MaxFailedAttempts - currentAttempts

				// Lockout action when limit is reached
				if currentAttempts >= MaxFailedAttempts {
					delete(userSessions, senderKey)
					sessionMutex.Unlock()

					fmt.Printf("🔒 [Security] Vendor %s locked out after %d failed password attempts.\n", session.User.Email, MaxFailedAttempts)

					reply := fmt.Sprintf("🛑 *Security Alert: Login Locked Out*\n\nYou have entered an incorrect password %d times. For security, your session has been terminated.\n\n🔑 Reset your password here:\n%s/forgot-password\n\nReply *login* to try again when ready.", MaxFailedAttempts, WebDashboardURL)
					s.sendTextMessage(toJID, reply)
					return
				}
				sessionMutex.Unlock()

				fmt.Printf("❌ [Auth Failed] Password mismatch for %s (Attempt %d/%d)\n", session.User.Email, currentAttempts, MaxFailedAttempts)

				reply := fmt.Sprintf("❌ Incorrect password. You have *%d attempt(s)* remaining.\n\n🔑 Reset password:\n%s/forgot-password\n\nReply *cancel* to exit.", attemptsLeft, WebDashboardURL)
				s.sendTextMessage(toJID, reply)
				return
			}

			// Reset failed attempts on successful login
			sessionMutex.Lock()
			session.FailedPasswordAttempts = 0
			session.State = "LOGGED_IN"
			sessionMutex.Unlock()

			fmt.Printf("✅ [Auth Success] Vendor %s logged in!\n", session.Vendor.BusinessName)

			reply := fmt.Sprintf("🎉 *Login Successful!* Welcome back, *%s*.\n\n", session.Vendor.BusinessName)
			reply += "📋 *Vendor Dashboard Menu:*\n"
			reply += "1️⃣ View My Active Listings\n"
			reply += "2️⃣ Pause / Activate a Service\n"
			reply += "3️⃣ Add a New Service or Product\n"
			reply += "0️⃣ Logout\n\n"
			reply += "👉 *Reply with an option number.*"

			s.sendTextMessage(toJID, reply)
			return

		// STEP 3: LOGGED IN VENDOR DASHBOARD
		case "LOGGED_IN":
			switch clean {
			case "1", "listings":
				var listings []models.Listing
				s.DB.Where("vendor_id = ?", session.Vendor.ID).Order("created_at desc").Find(&listings)

				if len(listings) == 0 {
					s.sendTextMessage(toJID, "⚠️ You currently have no listings. Create one on the Web Dashboard.")
					return
				}

				s.sendTextMessage(toJID, fmt.Sprintf("📊 *Your Listings (%d total):*\n", len(listings)))

				for i, item := range listings {
					itemNum := i + 1
					statusIcon := "🟢"
					statusText := "Active"
					if item.Status != "active" {
						statusIcon = "🔴"
						statusText = "Paused"
					}

					caption := fmt.Sprintf("%d️⃣ *%s*\n", itemNum, item.Title)
					caption += fmt.Sprintf("📌 *Status:* %s %s\n", statusIcon, statusText)
					caption += fmt.Sprintf("💰 *Price:* KES %.2f\n", item.Price)
					if item.Location != "" {
						caption += fmt.Sprintf("📍 *Location:* %s\n", item.Location)
					}

					sentImage := false
					if item.ImageURL != "" {
						if err := s.SendImage(toJID, item.ImageURL, caption); err == nil {
							sentImage = true
						} else {
							fmt.Printf("[WhatsApp] Failed to send image (%s): %v. Fallback to text...\n", item.ImageURL, err)
						}
					}

					if !sentImage {
						s.sendTextMessage(toJID, caption)
					}

					// Stagger card dispatches for vendor listing review
					if i < len(listings)-1 {
						time.Sleep(time.Duration(1200+rand.Intn(1000)) * time.Millisecond)
					}
				}

				s.sendTextMessage(toJID, "\n✏️ *To Pause/Activate:* Reply with *toggle 1* (or item number).\n🚪 Reply *0* to Logout.")
				return

			case "2", "toggle":
				s.sendTextMessage(toJID, "✏️ Reply with *toggle <number>* to change status (e.g., *toggle 1* or *toggle 2*).")
				return

			case "3", "add", "create", "new":
				reply := "➕ *Add a New Service or Product*\n\n"
				reply += "To upload photos, add sub-services, and set descriptions, visit your Web Dashboard:\n\n"
				reply += fmt.Sprintf("🌐 *Add Listing:* %s/dashboard/listings/new\n\n", WebDashboardURL)
				reply += "💡 *Tip:* Any listing created on the web will immediately be searchable on WhatsApp!"
				s.sendTextMessage(toJID, reply)
				return

			case "0", "logout":
				sessionMutex.Lock()
				delete(userSessions, senderKey)
				sessionMutex.Unlock()
				s.sendTextMessage(toJID, "👋 Logged out of Vendor Portal. You are back in Buyer Mode.")
				return

			default:
				if strings.HasPrefix(clean, "toggle ") {
					numStr := strings.TrimSpace(strings.TrimPrefix(clean, "toggle "))
					itemNum, err := strconv.Atoi(numStr)

					var listings []models.Listing
					s.DB.Where("vendor_id = ?", session.Vendor.ID).Order("created_at desc").Find(&listings)

					if err != nil || itemNum < 1 || itemNum > len(listings) {
						s.sendTextMessage(toJID, fmt.Sprintf("⚠️ Invalid listing number. Please choose a number between 1 and %d.", len(listings)))
						return
					}

					selectedItem := listings[itemNum-1]
					newStatus := "active"
					if selectedItem.Status == "active" {
						newStatus = "paused"
					}

					s.DB.Model(&selectedItem).Update("status", newStatus)

					statusIcon := "🟢"
					if newStatus != "active" {
						statusIcon = "🔴"
					}

					s.sendTextMessage(toJID, fmt.Sprintf("✅ Listing *'%s'* status updated to %s *%s*!", selectedItem.Title, statusIcon, newStatus))
					return
				}

				s.sendTextMessage(toJID, "💡 *Vendor Options:* Reply *1* for Listings, *toggle <number>* to Pause/Activate, or *0* to Logout.")
				return
			}
		}
	}

	// -------------------------------------------------------------
	// 2. BUYER & SYSTEM ROUTING
	// -------------------------------------------------------------
	switch {

	// A. Vendor Login Command
	case clean == "login" || clean == "vendor":
		sessionMutex.Lock()
		userSessions[senderKey] = &VendorSession{State: "AWAITING_IDENTIFIER"}
		sessionMutex.Unlock()

		reply := "🔒 *AskMtaa Vendor Login*\n\n"
		reply += "Please reply with your registered *Phone Number* (e.g., `0712345678`) or *Email address*:\n\n"
		reply += fmt.Sprintf("🔑 Don't have a vendor account yet? Register here:\n%s/register", WebDashboardURL)

		s.sendTextMessage(toJID, reply)
		return

	// B. Pagination ("more", "next")
	case clean == "more" || clean == "next":
		sessionMutex.RLock()
		sess, exists := userSessions[senderKey]
		sessionMutex.RUnlock()

		if !exists || sess == nil || len(sess.LastSearchResults) == 0 {
			s.sendTextMessage(toJID, "🛒 No active search session.\n\nReply *menu* to search for products or services.")
			return
		}

		sessionMutex.Lock()
		sess.CurrentPage++
		nextPage := sess.CurrentPage
		listings := sess.LastSearchResults
		header := sess.LastSearchHeader
		sessionMutex.Unlock()

		s.renderAndSendListings(toJID, listings, header, nextPage)
		return

	// C. Greetings & Main Category Menu
	case strings.Contains(clean, "hi"), strings.Contains(clean, "hello"), clean == "menu", clean == "0":
		if len(categories) == 0 {
			s.sendTextMessage(toJID, "👋 Welcome to AskMtaa! No categories are currently available.")
			return
		}

		reply := "Habari! Welcome to AskMtaa local service finder 🇰🇪.\nWhich category are you looking for today?\n"
		for i, cat := range categories {
			reply += fmt.Sprintf("\n*%d. %s*", i+1, cat.Name)
		}
		reply += "\n\n💡 *Or reply with a search term* (e.g., _laundry in Embu_ or _wifi_)."
		reply += "\n👉 *Reply with a number to choose a category.*"

		s.sendTextMessage(toJID, reply)
		return

	// D. Help Guide
	case strings.Contains(clean, "help"), clean == "?":
		reply := "❓ *AskMtaa How-To Guide*\n\n"
		reply += "👉 *For Buyers:*\n"
		reply += "• Reply *menu* - View service categories\n"
		reply += "• Reply *1, 2, 3...* - Browse items by category number\n"
		reply += "• Type any service/location - Search directly (e.g., *boda*, *laundry*)\n"
		reply += "• Reply *more* or *next* - Load next page of search results\n\n"
		reply += "💼 *For Vendors:*\n"
		reply += "• Reply *login* or *vendor* - Access Vendor Portal"

		s.sendTextMessage(toJID, reply)
		return

	// E. About Us
	case strings.Contains(clean, "about"):
		s.sendTextMessage(toJID, "ℹ️ *What is AskMtaa?*\n\nAskMtaa is a hyper-local digital marketplace connecting local neighborhood vendors directly with customers.")
		return

	// F. Customer Support
	case strings.Contains(clean, "support"):
		s.sendTextMessage(toJID, "📞 Support team has been notified! A representative will get back to you shortly or contact Dan 0757252830 / 0758079624")
		return

	// G. Default Fallback: Category Selection (by Number) OR Keyword Search
	default:
		// 1. Category Selection by Number
		num, err := strconv.Atoi(clean)
		if err == nil && num >= 1 && num <= len(categories) {
			selectedCategory := categories[num-1]

			var listings []models.Listing
			s.DB.Preload("Vendor").Preload("SubServices").
				Where("status = ? AND category_id = ?", "active", selectedCategory.ID).
				Order("created_at desc").
				Find(&listings)

			if len(listings) == 0 {
				s.sendTextMessage(toJID, fmt.Sprintf("📂 No active listings found under *%s* right now.\n\nReply *menu* to see all categories.", selectedCategory.Name))
				return
			}

			header := fmt.Sprintf("📂 *Listings in %s:*", selectedCategory.Name)

			// Store in memory & render Page 1
			s.displayAndCacheListings(senderKey, toJID, listings, header)
			return
		}

		// 2. Keyword Search Fallback
		var listings []models.Listing
		term := "%" + clean + "%"
		s.DB.Preload("Vendor").Preload("SubServices").
			Where("status = ?", "active").
			Where("LOWER(title) LIKE ? OR LOWER(description) LIKE ? OR LOWER(location) LIKE ?", term, term, term).
			Order("created_at desc").
			Find(&listings)

		if len(listings) == 0 {
			s.sendTextMessage(toJID, fmt.Sprintf("🔍 No results found for '*%s*'.\n\nReply *menu* to view categories.", clean))
			return
		}

		header := fmt.Sprintf("🔎 *Search results for '%s':*", clean)

		// Store in memory & render Page 1
		s.displayAndCacheListings(senderKey, toJID, listings, header)
		return
	}
}

// Helper: Saves search results to the buyer's session and renders Page 1
func (s *WhatsAppService) displayAndCacheListings(senderKey string, toJID types.JID, listings []models.Listing, header string) {
	sessionMutex.Lock()
	session, exists := userSessions[senderKey]
	if !exists || session == nil {
		session = &VendorSession{State: "BUYER"}
		userSessions[senderKey] = session
	}

	// Cache results for pagination
	session.LastSearchResults = listings
	session.LastSearchHeader = header
	session.CurrentPage = 1
	sessionMutex.Unlock()

	// Render Page 1
	s.renderAndSendListings(toJID, listings, header, 1)
}

// Renders listings with Pagination, Sub-Services, and Prices
func (s *WhatsAppService) renderAndSendListings(toJID types.JID, listings []models.Listing, header string, page int) {
	total := len(listings)
	if total == 0 {
		s.sendTextMessage(toJID, "🛒 No matching services found.\n\nReply *menu* to view categories or try another search.")
		return
	}

	// 1. Calculate Page Bounds
	if page < 1 {
		page = 1
	}
	start := (page - 1) * PageSize
	if start >= total {
		s.sendTextMessage(toJID, "⚠️ You have reached the end of the search results.\n\nReply *menu* to view categories.")
		return
	}

	end := start + PageSize
	if end > total {
		end = total
	}

	totalPages := (total + PageSize - 1) / PageSize
	pageListings := listings[start:end]

	// 2. Send Page Header
	pageHeader := fmt.Sprintf("%s\n📄 *Page %d of %d (Showing %d-%d of %d)*", header, page, totalPages, start+1, end, total)
	s.sendTextMessage(toJID, pageHeader)

	// 3. Loop through ONLY current page listings
	for i, listing := range pageListings {
		itemNum := start + i + 1
		caption := fmt.Sprintf("%d. *%s*", itemNum, listing.Title)

		if listing.Price > 0 {
			caption += fmt.Sprintf("\n💰 *Starting Price:* KES %.2f", listing.Price)
		}

		if listing.Location != "" {
			caption += fmt.Sprintf("\n📍 *Location:* %s", listing.Location)
		}

		if listing.Vendor.BusinessName != "" {
			caption += fmt.Sprintf("\n🏢 *Seller:* %s", listing.Vendor.BusinessName)
		}

		if len(listing.SubServices) > 0 {
			caption += "\n\n🛠️ *Sub-Services & Options:*"
			for _, sub := range listing.SubServices {
				if sub.Price > 0 {
					caption += fmt.Sprintf("\n  ▫️ %s — KES %.2f", sub.Name, sub.Price)
				} else {
					caption += fmt.Sprintf("\n  ▫️ %s", sub.Name)
				}
			}
		}

		chatPhone := listing.Vendor.WhatsAppNumber
		if chatPhone == "" {
			chatPhone = listing.Vendor.PhoneNumber
		}

		callPhone := listing.Vendor.PhoneNumber
		if callPhone == "" {
			callPhone = listing.Vendor.WhatsAppNumber
		}

		if chatPhone != "" {
			caption += fmt.Sprintf("\n\n📲 *Chat Seller:* %s", formatSellerLink(chatPhone, listing.Title))
		}
		if callPhone != "" {
			caption += fmt.Sprintf("\n📞 *Call:* %s", callPhone)
		}

		sentImage := false

		if listing.ImageURL != "" {
			if err := s.SendImage(toJID, listing.ImageURL, caption); err == nil {
				sentImage = true
			} else {
				fmt.Printf("[WhatsApp] Failed to send image (%s): %v. Fallback to text...\n", listing.ImageURL, err)
			}
		}

		if !sentImage {
			s.sendTextMessage(toJID, caption)
		}

		// Asynchronous non-blocking lead tracking (won't hold up execution)
		go s.recordVendorInAppLead(listing)

		// 🛡️ ANTI-BAN STAGGERING: Add a jittered 1.5s - 3.0s pause between sending listing cards
		if i < len(pageListings)-1 {
			cardPause := 1500 + rand.Intn(1500)
			time.Sleep(time.Duration(cardPause) * time.Millisecond)
		}
	}

	// 4. Send Pagination Footer Prompt
	if end < total {
		remaining := total - end
		nextCount := PageSize
		if remaining < PageSize {
			nextCount = remaining
		}
		footer := fmt.Sprintf("👉 Reply *more* or *next* to see the next %d listing(s).\n👉 Reply *menu* or *0* for Categories.", nextCount)
		s.sendTextMessage(toJID, footer)
	} else {
		s.sendTextMessage(toJID, "🏁 *End of search results.*\n👉 Reply *menu* or *0* to return to Categories.")
	}
}

// recordVendorInAppLead records in-app alerts safely without spamming WhatsApp
func (s *WhatsAppService) recordVendorInAppLead(item models.Listing) {
	var vendorIDStr string

	if item.VendorID != uuid.Nil {
		vendorIDStr = item.VendorID.String()
	} else if item.Vendor.ID != uuid.Nil {
		vendorIDStr = item.Vendor.ID.String()
	}

	if vendorIDStr == "" {
		return
	}

	// 1. Verify if vendor exists and has WhatsApp Opt-In enabled
	var vendor models.Vendor
	if err := s.DB.Where("id = ?", vendorIDStr).First(&vendor).Error; err != nil {
		return
	}

	if !vendor.WhatsAppOptIn {
		// Vendor has opted out of WhatsApp leads/alerts
		return
	}

	// 2. Log in-app lead notification
	notification := models.Notification{
		ID:        uuid.New().String(),
		VendorID:  vendorIDStr,
		Title:     "New Customer Lead",
		Message:   fmt.Sprintf("A buyer on WhatsApp viewed your listing '%s' (KES %.2f)", item.Title, item.Price),
		Type:      "lead",
		IsRead:    false,
		CreatedAt: time.Now(),
	}

	if err := s.DB.Create(&notification).Error; err != nil {
		fmt.Printf("⚠️ [In-App Lead] Failed to log notification for vendor %s: %v\n", vendorIDStr, err)
	} else {
		fmt.Printf("🔔 [In-App Lead Logged] Vendor %s notified on dashboard.\n", vendorIDStr)
	}
}
// Helper: Fetches image bytes whether imageURL is a remote URL (Cloudinary/S3) or local file
func fetchOrReadImageBytes(imageURL string) ([]byte, error) {
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" {
		return nil, fmt.Errorf("empty image URL")
	}

	// 1. Handle Remote Cloudinary or HTTP/HTTPS URLs
	if strings.HasPrefix(imageURL, "http://") || strings.HasPrefix(imageURL, "https://") {
		fetchURL := imageURL

		// Convert Cloudinary f_auto or .webp into JPEG for WhatsApp compatibility
		if strings.Contains(fetchURL, "cloudinary.com") {
			if strings.Contains(fetchURL, "f_auto") {
				fetchURL = strings.Replace(fetchURL, "f_auto", "f_jpg", 1)
			} else if !strings.Contains(fetchURL, "f_jpg") {
				fetchURL = strings.Replace(fetchURL, "/image/upload/", "/image/upload/f_jpg/", 1)
			}
		}

		resp, err := httpClient.Get(fetchURL)
		if err != nil {
			return nil, fmt.Errorf("failed to download remote image from %s: %w", fetchURL, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d error downloading image from %s", resp.StatusCode, fetchURL)
		}

		return io.ReadAll(resp.Body)
	}

	// 2. Fallback to Local Disk Path
	localPath := getLocalImagePath(imageURL)
	return os.ReadFile(localPath)
}

// SendImage downloads remote Cloudinary images or reads local files and sends to WhatsApp
func (s *WhatsAppService) SendImage(toJID types.JID, imageURL string, caption string) error {
	imgData, err := fetchOrReadImageBytes(imageURL)
	if err != nil {
		return fmt.Errorf("failed to read/download image (%s): %w", imageURL, err)
	}

	resp, err := s.Client.Upload(context.Background(), imgData, whatsmeow.MediaImage)
	if err != nil {
		return fmt.Errorf("failed to upload image to WhatsApp servers: %w", err)
	}

	mimeType := http.DetectContentType(imgData)

	msg := &waE2E.Message{
		ImageMessage: &waE2E.ImageMessage{
			Caption:       proto.String(caption),
			Mimetype:      proto.String(mimeType),
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			FileSHA256:    resp.FileSHA256,
			FileEncSHA256: resp.FileEncSHA256,
			FileLength:    proto.Uint64(uint64(len(imgData))),
		},
	}

	_, err = s.Client.SendMessage(context.Background(), toJID, msg)
	return err
}

func (s *WhatsAppService) sendTextMessage(toJID types.JID, text string) error {
	_, err := s.Client.SendMessage(context.Background(), toJID, &waE2E.Message{
		Conversation: proto.String(text),
	})
	return err
}

func isSystemCommand(text string) bool {
	commands := []string{"hi", "hello", "menu", "0", "about", "support", "categories", "latest"}
	for _, cmd := range commands {
		if text == cmd {
			return true
		}
	}
	return false
}

func parsePhoneToJID(phone string) (types.JID, bool) {
	re := regexp.MustCompile(`\D`)
	clean := re.ReplaceAllString(phone, "")

	if strings.HasPrefix(clean, "0") && len(clean) == 10 {
		clean = "254" + clean[1:]
	}

	if clean == "" {
		return types.JID{}, false
	}

	return types.NewJID(clean, "s.whatsapp.net"), true
}

func formatSellerLink(phone string, listingTitle string) string {
	clean := strings.TrimSpace(phone)
	clean = strings.ReplaceAll(clean, "+", "")
	clean = strings.ReplaceAll(clean, " ", "")

	if strings.HasPrefix(clean, "0") && len(clean) == 10 {
		clean = "254" + clean[1:]
	}

	presetMsg := fmt.Sprintf("Hi! I saw your listing '%s' on AskMtaa and I'd like to inquire about it.", listingTitle)
	return fmt.Sprintf("https://wa.me/%s?text=%s", clean, url.QueryEscape(presetMsg))
}

func getLocalImagePath(imageURL string) string {
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" {
		return ""
	}

	if strings.HasPrefix(imageURL, "http://") || strings.HasPrefix(imageURL, "https://") {
		parts := strings.Split(imageURL, "/uploads/")
		if len(parts) > 1 {
			imageURL = "uploads/" + parts[1]
		}
	}

	imageURL = strings.TrimPrefix(imageURL, "/")

	if !strings.HasPrefix(imageURL, "./") {
		imageURL = "./" + imageURL
	}

	return imageURL
}

func fileExists(filename string) bool {
	if filename == "" {
		return false
	}
	info, err := os.Stat(filename)
	if err != nil {
		return false
	}
	return !info.IsDir()
}