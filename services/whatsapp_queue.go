package services

import (
	"fmt"
	"log"
	"math/rand"
	
	"time"
)

type WhatsAppJob struct {
	PhoneNumber string
	Message     string
	Attempts    int
}

// Queue capacity of 100 messages to handle traffic spikes safely
var WhatsAppQueue = make(chan WhatsAppJob, 100)

// InitWhatsAppWorker launches the background goroutine worker
func InitWhatsAppWorker() {
	// Seed random generator for unpredictable delays
	rand.Seed(time.Now().UnixNano())

	go func() {
		log.Println("🚀 WhatsApp background rate-limiter worker running...")

		for job := range WhatsAppQueue {
			// 1. Minimum 8 seconds delay + random 1-10 seconds jitter (8s to 18s total)
			randomJitter := rand.Intn(10) + 1
			delayDuration := time.Duration(8+randomJitter) * time.Second

			log.Printf("⏳ [WhatsApp Queue] Delaying send to %s for %v...", job.PhoneNumber, delayDuration)
			time.Sleep(delayDuration)

			// 2. Dispatch message
			err := dispatchWhatsAppMessage(job.PhoneNumber, job.Message)
			if err != nil {
				log.Printf("❌ [WhatsApp Queue] Error sending to %s: %v", job.PhoneNumber, err)

				// Retry logic: Allow up to 2 retries on temporary failures
				if job.Attempts < 2 {
					job.Attempts++
					log.Printf("🔁 [WhatsApp Queue] Re-queuing job for %s (Attempt %d)", job.PhoneNumber, job.Attempts+1)
					time.Sleep(15 * time.Second) // Wait 15s before re-enqueueing
					WhatsAppQueue <- job
				}
			} else {
				log.Printf("✅ [WhatsApp Queue] Delivered notification to %s", job.PhoneNumber)
			}
		}
	}()
}

// EnqueueNotification adds a message to the buffer without blocking Gin handlers
func EnqueueNotification(phoneNumber string, message string) {
	job := WhatsAppJob{
		PhoneNumber: phoneNumber,
		Message:     message,
		Attempts:    0,
	}

	select {
	case WhatsAppQueue <- job:
		log.Printf("📥 [WhatsApp Queue] Successfully enqueued message for %s", phoneNumber)
	default:
		log.Printf("⚠️ [WhatsApp Queue] Queue buffer full! Message dropped for %s", phoneNumber)
	}
}

func dispatchWhatsAppMessage(phone string, message string) error {
	if GlobalWhatsAppService == nil {
		return fmt.Errorf("WhatsApp service instance is not initialized yet")
	}

	// Dispatch using whatsmeow via GlobalWhatsAppService
	return GlobalWhatsAppService.SendTextMessagePublic(phone, message)
}