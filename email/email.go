package email

import (
	"fmt"
	"log"
	"net/smtp"
	"os"
	"github.com/joho/godotenv" // Make sure this is imported
)

func SendWelcomeEmail(toEmail string, vendorName string) error {
	// Load configuration from environment variables
	if err := godotenv.Load(); err != nil {
        log.Println("Warning: .env file not found, relying on system environment variables")
    }
	smtpHost := os.Getenv("SMTP_HOST")     // e.g., "smtp.gmail.com"
	smtpPort := os.Getenv("SMTP_PORT")     // e.g., "587"
	senderEmail := os.Getenv("SMTP_EMAIL") // e.g., "your-email@gmail.com"
	senderPassword := os.Getenv("SMTP_PASSWORD") // App password or SMTP password

	// Setup authentication
	auth := smtp.PlainAuth("", senderEmail, senderPassword, smtpHost)
   
	// Compose the email headers and body
    subject := "Subject: Welcome to ASKMTAA Platform!\r\n"
    mime := "MIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n"
    
    body := fmt.Sprintf(`
        <html>
        <body>
            <h2>Welcome, %s!</h2>
            <p>Your vendor account has been successfully created. We are thrilled to have you on board.</p>
            <p>You can now log in and start managing your dashboard.</p>
            <br>
            <p>Best regards,<br>
               <strong> Waweru Dan</strong><br>
               <a href="tel:0758079624">0758079624</a>
            </p>
        </body>
        </html>
    `, vendorName)

	msg := []byte(subject + mime + "\r\n" + body)

	// Send the email
	addr := fmt.Sprintf("%s:%s", smtpHost, smtpPort)
	err := smtp.SendMail(addr, auth, senderEmail, []string{toEmail}, msg)
	if err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}

	return nil
}
func SendPasswordResetOTP(toEmail string, otp string) error {
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: Could not load .env file")
	}
	
    smtpHost := os.Getenv("SMTP_HOST")
    smtpPort := os.Getenv("SMTP_PORT")
    senderEmail := os.Getenv("SMTP_EMAIL")
    senderPassword := os.Getenv("SMTP_PASSWORD")

    auth := smtp.PlainAuth("", senderEmail, senderPassword, smtpHost)

    subject := "Subject: Your ASKMTAA Password Reset Code\r\n"
    mime := "MIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n"

    body := fmt.Sprintf(`
        <html>
        <body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
            <h2>Password Reset Code</h2>
            <p>You requested a password reset for your <strong>ASKMTAA</strong> account.</p>
            <p>Your 6-digit verification code is:</p>
            <div style="background-color: #f3f4f6; padding: 15px; border-radius: 8px; text-align: center; width: 200px; margin: 15px 0;">
                <span style="font-size: 28px; font-weight: bold; letter-spacing: 6px; color: #059669;">%s</span>
            </div>
            <p>This code will expire in <strong>10 minutes</strong>.</p>
            <p>If you did not request this, please ignore this email.</p>
            <br>
            <p>For Support ,<br>
               <a href="tel:0758079624">0758079624</a>
            </p>
        </body>
        </html>
    `, otp)

    msg := []byte(subject + mime + "\r\n" + body)
    addr := fmt.Sprintf("%s:%s", smtpHost, smtpPort)

    return smtp.SendMail(addr, auth, senderEmail, []string{toEmail}, msg)
}