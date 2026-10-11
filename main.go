package main

import (
	"log"
	"time"
    "github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"main/config"
	"main/handlers"
	"main/middleware"
	"main/models"
	"net/http" // 👈 ADD THIS LINE
	"main/repository"
	"main/services"
)

func main() {
	// 1. Load Environment Variables
	if err := godotenv.Load(); err != nil {
		log.Println("⚠️ Warning: .env file not found")
	}

	// 2. Initialize Database FIRST
	config.ConnectDatabase()

	// 3. Initialize WhatsApp Background Queue Worker & Bot
	services.InitWhatsAppWorker()
	go func() {
		if _, err := services.InitWhatsApp(config.DB); err != nil {
			log.Printf("⚠️ Failed to initialize WhatsApp bot: %v", err)
		}
	}()

	// 4. Database Auto-Migration
	if err := config.DB.Exec("SET FOREIGN_KEY_CHECKS = 0;").Error; err != nil {
		log.Fatalf("❌ Failed to disable foreign key checks: %v", err)
	}

	err := config.DB.AutoMigrate(
		&models.User{},
		&models.Vendor{},
		&models.Listing{},
		&models.SubService{},
		&models.Category{},
		&models.Notification{},
	)
	if err != nil {
		log.Fatalf("❌ Failed to run migrations: %v", err)
	}

	if err := config.DB.Exec("SET FOREIGN_KEY_CHECKS = 1;").Error; err != nil {
		log.Fatalf("❌ Failed to re-enable foreign key checks: %v", err)
	}

	// 5. Repositories & Handlers
	authRepo := repository.NewAuthRepository(config.DB)
	vendorRepo := repository.NewVendorRepository(config.DB)

	authHandler := handlers.NewAuthHandler(authRepo)
	vendorHandler := handlers.NewVendorHandler(vendorRepo)

	// 6. Router Setup
	r := gin.Default()
	r.Static("/uploads", "./uploads")

	// 7. CORS Settings
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"https://askmtaa.vercel.app", "http://localhost:4200"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))
  r.GET("/health", func(c *gin.Context) {
    c.JSON(http.StatusOK, gin.H{"status": "ok"})
})
	// 8. API Routes
	v1 := r.Group("/api")
	{
		auth := v1.Group("/auth")
		{
			auth.POST("/register", authHandler.Register)
			auth.POST("/login", authHandler.Login)
			auth.POST("/forgot-password", authHandler.ForgotPassword)
			auth.POST("/reset-password", authHandler.ResetPassword)
		}

		v1.GET("/listings", vendorHandler.GetPublicListings)

		vendor := v1.Group("/vendor")
		vendor.Use(middleware.AuthMiddleware())
		{
			vendor.GET("/vendor", vendorHandler.GetAllVendors)
			vendor.GET("/dashboard", vendorHandler.GetDashboardData)
			
			vendor.GET("/categories", vendorHandler.GetCategories)
			vendor.PUT("/categories/:id", vendorHandler.UpdateCategory)
			vendor.POST("/categories", vendorHandler.CreateCategory)
			vendor.DELETE("/categories/:id", vendorHandler.DeleteCategory)
			vendor.GET("/listings", vendorHandler.GetListings)
			vendor.POST("/listings", vendorHandler.CreateListing)
			vendor.PATCH("/listings/:id/status", vendorHandler.UpdateListingStatus)
			vendor.PUT("/listings/:id", vendorHandler.UpdateListing)
			vendor.DELETE("/listings/:id", vendorHandler.DeleteListing)
			vendor.POST("/upload", vendorHandler.UploadImage)
			vendor.PUT("/settings", vendorHandler.UpdateSettings)
			// Notification Endpoints
	vendor.GET("/notifications", vendorHandler.GetNotifications)
	vendor.GET("/notifications/unread-count", vendorHandler.GetUnreadNotificationCount)
	vendor.PATCH("/notifications/:id/read", vendorHandler.MarkNotificationRead)
	vendor.PATCH("/notifications/read-all", vendorHandler.MarkAllNotificationsRead)

	// Settings & WhatsApp Opt-In
	vendor.PATCH("/settings/whatsapp-optin", vendorHandler.ToggleWhatsAppOptIn)
		}
	}

	log.Println("🚀 Server running on http://localhost:8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("❌ Failed to start server: %v", err)
	}
}