package cron

import (
	"main/services" // Adjust to your module path
	"fmt"
	"log"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

type PendingSummary struct {
	Phone       string
	UnreadCount int
}

func StartNotificationCron(db *gorm.DB) {
	c := cron.New()

	// Cron Expression: "0 8,12,18,22 * * *"
	// Runs at minute 0 of hours 8 (8 AM), 12 (12 PM), 18 (6 PM), and 22 (10 PM)
	_, err := c.AddFunc("0 8,12,18,22 * * *", func() {
		log.Println("⏰ Running scheduled WhatsApp inquiry digest check...")
		sendPendingReminders(db)
	})

	if err != nil {
		log.Printf("❌ Failed to register Cron job: %v", err)
		return
	}

	c.Start()
	log.Println("📅 Scheduled WhatsApp Cron service initialized.")
}

func sendPendingReminders(db *gorm.DB) {
	var summaries []PendingSummary

	// Query vendors who have UNREAD notifications
	query := `
		SELECT v.phone, COUNT(n.id) as unread_count
		FROM vendors v
		JOIN notifications n ON n.vendor_id = v.id
		WHERE n.is_read = false
		GROUP BY v.id, v.phone
		HAVING unread_count > 0
	`
	db.Raw(query).Scan(&summaries)

	for _, item := range summaries {
		msg := fmt.Sprintf(
			"🔔 *AskMtaa Reminder*\n\nYou have *%d unread customer inquiry/inquiries* waiting on your dashboard.\n\n👉 Log in to reply: https://askmtaa.com/vendor/dashboard",
			item.UnreadCount,
		)

		// Enqueue job into your delay queue so dispatches trickle out safely
		services.EnqueueNotification(item.Phone, msg)
	}
}