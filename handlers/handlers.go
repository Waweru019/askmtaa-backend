package handlers

import (
	
	"net/http"
	"fmt"
	"time"
	"log"
	"errors"
    "strings"
	"main/middleware"
	"gorm.io/gorm"
	"main/models"
	
	"main/repository"
	"os"
	

	"github.com/cloudinary/cloudinary-go/v2"
	"github.com/cloudinary/cloudinary-go/v2/api/uploader"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// -----------------------------------------------------------------------------
// DTOs (Data Transfer Objects)
// -----------------------------------------------------------------------------

type CreateListingDTO struct {
	Title           string              `json:"title" binding:"required"`
	Description     string              `json:"description" binding:"required"`
	ListingType     string              `json:"listingType" binding:"required"`
	CategoryID      string              `json:"categoryId" binding:"required"`
	Price           float64             `json:"price"`
	PriceType       string              `json:"priceType"`
	ImageURL        string              `json:"imageUrl"`
	Location        string              `json:"location"`
	ExactAddress    string              `json:"exactAddress"`
	Latitude        *float64            `json:"latitude"`
	Longitude       *float64            `json:"longitude"`
	StartDate       string              `json:"startDate"` // Changed from *time.Time to string
	EndDate         string              `json:"endDate"`   // Changed from *time.Time to string
	MeetingPoint    string              `json:"meetingPoint"`
	NetworkProvider string              `json:"networkProvider"`
	Instructions    string              `json:"instructions"`
	SubServices     []models.SubService `json:"subServices"`
}

type UpdateSettingsDTO struct {
	BusinessName    string `json:"businessName" binding:"required"`
	PhoneNumber     string `json:"phoneNumber"`
	WhatsAppNumber  string `json:"whatsAppNumber"`
	DefaultLocation string `json:"defaultLocation"`
}

type UpdateStatusDTO struct {
	Status string `json:"status" binding:"required"`
}

// -----------------------------------------------------------------------------
// Auth Handler
// -----------------------------------------------------------------------------

type AuthHandler struct {
	repo repository.AuthRepository
}

func NewAuthHandler(repo repository.AuthRepository) *AuthHandler {
	return &AuthHandler{repo: repo}
}

// POST /api/auth/register
func (h *AuthHandler) Register(c *gin.Context) {
	var input repository.RegisterInput

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid input data",
			"details": err.Error(),
		})
		return
	}

	existingEmail, err := h.repo.FindByEmail(input.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error while checking email"})
		return
	}
	if existingEmail != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "A user with this email already exists"})
		return
	}

	existingPhone, err := h.repo.FindByPhone(input.PhoneNumber)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error while checking phone number"})
		return
	}
	if existingPhone != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "A user with this phone number already exists"})
		return
	}

	user, vendor, err := h.repo.RegisterVendor(input)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to register user",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Vendor registered successfully",
		"data": gin.H{
			"user":   user,
			"vendor": vendor,
		},
	})
}

// POST /api/auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var input repository.LoginInput

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input", "details": err.Error()})
		return
	}

	user, err := h.repo.Login(input)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	token, err := middleware.GenerateJWT(user.ID, user.Vendor.ID, user.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate access token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Login successful",
		"token":   token,
		"user": gin.H{
			"id":          user.ID,
			"email":       user.Email,
			"phoneNumber": user.PhoneNumber,
			"role":        user.Role,
			"vendor":      user.Vendor,
		},
	})
}

// -----------------------------------------------------------------------------
// Vendor Handler
// -----------------------------------------------------------------------------

type VendorHandler struct {
	Repo repository.VendorRepository
	Cld  *cloudinary.Cloudinary
}

func NewVendorHandler(repo repository.VendorRepository) *VendorHandler {
	cldURL := os.Getenv("CLOUDINARY_URL")
	cld, err := cloudinary.NewFromURL(cldURL)
	if err != nil {
		log.Printf("⚠️ Warning: Could not initialize Cloudinary: %v", err)
	}
	return &VendorHandler{Repo: repo,
			Cld:  cld,}

}


func (h *VendorHandler) getAuthenticatedVendor(c *gin.Context) (*models.Vendor, error) {
	// Try vendorID (as set by middleware)
	if vendorIDVal, exists := c.Get("vendorID"); exists {
		if vendorID, ok := vendorIDVal.(uuid.UUID); ok && vendorID != uuid.Nil {
			return h.Repo.GetVendorByID(vendorID)
		}
	}

	// Try userID (as set by middleware)
	if userIDVal, exists := c.Get("userID"); exists {
		if userID, ok := userIDVal.(uuid.UUID); ok && userID != uuid.Nil {
			return h.Repo.GetVendorByUserID(userID)
		}
	}

	return nil, errors.New("unauthorized: no vendor found in token")
}

// GET /api/vendor/listings
func (h *VendorHandler) GetListings(c *gin.Context) {
    vendor, err := h.getAuthenticatedVendor(c)
    if err != nil {
        c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
        return
    }

    // Extract user role from context
    roleVal, _ := c.Get("role")
    role, _ := roleVal.(string)

    listings, err := h.Repo.GetListingsByVendor(vendor.ID, role)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch listings"})
        return
    }

    c.JSON(http.StatusOK, listings)
}
// GET /api/vendor/listings
// GET /api/listings (Public - No Authorization Required)
func (h *VendorHandler) GetPublicListings(c *gin.Context) {
	listings, err := h.Repo.GetPublicListings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch listings"})
		return
	}

	c.JSON(http.StatusOK, listings)
}
func (h *VendorHandler) CreateListing(c *gin.Context) {
	vendor, err := h.getAuthenticatedVendor(c) // Or getVendorIDFromContext(c) depending on your setup
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Vendor not found"})
		return
	}

	var dto CreateListingDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		log.Printf("❌ Binding Error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input payload", "details": err.Error()})
		return
	}

	categoryUUID, err := uuid.Parse(dto.CategoryID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid category ID format"})
		return
	}

	finalLocation := dto.Location
	if finalLocation == "" {
		finalLocation = vendor.DefaultLocation
	}

	var parsedStartDate *time.Time
	if dto.StartDate != "" {
		t, err := time.Parse("2006-01-02", dto.StartDate)
		if err == nil {
			parsedStartDate = &t
		} else {
			t, err = time.Parse(time.RFC3339, dto.StartDate)
			if err == nil {
				parsedStartDate = &t
			}
		}
	}

	// =========================================================================
	// 🌟 SEO IMAGE URL GENERATION
	// Transforms raw Cloudinary URL + dto.Title into an SEO-optimized URL
	// Example: .../listings/xnktb6pwjjbox95wz5yr/2-bedroom-apartment.webp
	// =========================================================================
	seoImageURL := dto.ImageURL
	if seoImageURL != "" {
		seoImageURL = GenerateSEOImageUrl(seoImageURL, dto.Title)
	}

	listing := models.Listing{
		VendorID:        vendor.ID,
		CategoryID:      categoryUUID,
		Title:           dto.Title,
		Description:     dto.Description,
		ListingType:     dto.ListingType,
		Price:           dto.Price,
		PriceType:       dto.PriceType,
		ImageURL:        seoImageURL, // Store the SEO-enhanced image URL in DB
		Location:        finalLocation,
		StartDate:       parsedStartDate,
		MeetingPoint:    dto.MeetingPoint,
		NetworkProvider: dto.NetworkProvider,
		Instructions:    dto.Instructions,
		Status:          "active",
	}

	created, err := h.Repo.CreateListing(&listing, dto.SubServices)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, created)
}
// PATCH /api/vendor/listings/:id/status
func (h *VendorHandler) UpdateListingStatus(c *gin.Context) {
	listingIDStr := c.Param("id")
	listingID, err := uuid.Parse(listingIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid listing ID format"})
		return
	}

	var dto UpdateStatusDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.Repo.UpdateListingStatus(listingID, dto.Status); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update listing status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Listing status updated successfully"})
}
// PUT /api/vendor/listings/:id
func (h *VendorHandler) UpdateListing(c *gin.Context) {
	listingIDStr := c.Param("id")
	listingID, err := uuid.Parse(listingIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid listing ID format"})
		return
	}

	vendor, err := h.getAuthenticatedVendor(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Vendor not found"})
		return
	}

	var dto CreateListingDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload", "details": err.Error()})
		return
	}

	categoryUUID, err := uuid.Parse(dto.CategoryID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid category ID format"})
		return
	}

	var parsedStartDate *time.Time
	if dto.StartDate != "" {
		if t, err := time.Parse("2006-01-02", dto.StartDate); err == nil {
			parsedStartDate = &t
		} else if t, err := time.Parse(time.RFC3339, dto.StartDate); err == nil {
			parsedStartDate = &t
		}
	}

	listing := models.Listing{
		ID:              listingID,
		VendorID:        vendor.ID,
		CategoryID:      categoryUUID,
		Title:           dto.Title,
		Description:     dto.Description,
		ListingType:     dto.ListingType,
		Price:           dto.Price,
		PriceType:       dto.PriceType,
		ImageURL:        dto.ImageURL,
		Location:        dto.Location,
		ExactAddress:    dto.ExactAddress,
		Latitude:        dto.Latitude,
		Longitude:       dto.Longitude,
		StartDate:       parsedStartDate,
		MeetingPoint:    dto.MeetingPoint,
		NetworkProvider: dto.NetworkProvider,
		Instructions:    dto.Instructions,
		Status:          "active",
	}

	updated, err := h.Repo.UpdateListing(&listing, dto.SubServices)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update listing"})
		return
	}

	c.JSON(http.StatusOK, updated)
}


// DELETE /api/vendor/listings/:id
func (h *VendorHandler) DeleteListing(c *gin.Context) {
	listingIDStr := c.Param("id")
	listingID, err := uuid.Parse(listingIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid listing ID format"})
		return
	}

	if err := h.Repo.DeleteListing(listingID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete listing"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Listing deleted successfully"})
}

// PUT /api/vendor/settings
func (h *VendorHandler) UpdateSettings(c *gin.Context) {
	vendor, err := h.getAuthenticatedVendor(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var dto UpdateSettingsDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	phone := dto.WhatsAppNumber
	if phone == "" {
		phone = dto.PhoneNumber
	}

	vendor.BusinessName = dto.BusinessName
	vendor.WhatsAppNumber = phone
	vendor.PhoneNumber = phone
	vendor.DefaultLocation = dto.DefaultLocation

	if err := h.Repo.UpdateVendor(vendor); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update vendor profile"})
		return
	}

	c.JSON(http.StatusOK, vendor)
}

// GET /api/categories
func (h *VendorHandler) GetCategories(c *gin.Context) {
	categories, err := h.Repo.GetCategories()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch categories"})
		return
	}
	c.JSON(http.StatusOK, categories)
}
// DELETE /api/categories/:id
func (h *VendorHandler) DeleteCategory(c *gin.Context) {
    catID := c.Param("id")
    if catID == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Category ID is required"})
        return
    }

    err := h.Repo.DeleteCategory(catID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    c.JSON(http.StatusOK, gin.H{"message": "Category deleted successfully"})
}
// POST /api/vendor/categories
func (h *VendorHandler) CreateCategory(c *gin.Context) {
    var req struct {
        Name string `json:"name" binding:"required"`
        Slug string `json:"slug" binding:"required"`
        Type string `json:"type" binding:"required"`
    }

    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Name, slug, and type are required"})
        return
    }

    newCategory := repository.AdminCategory{
        ID:   uuid.New().String(),
        Name: req.Name,
        Slug: req.Slug,
        Type: req.Type,
    }

    if err := h.Repo.CreateCategory(&newCategory); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create category"})
        return
    }

    c.JSON(http.StatusCreated, newCategory)
}

// PUT /api/categories/:id
func (h *VendorHandler) UpdateCategory(c *gin.Context) {
	catID := c.Param("id")
	if catID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Category ID is required"})
		return
	}

	var req struct {
		Name string `json:"name" binding:"required"`
		Type string `json:"type" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
		return
	}

	// Generate slug from name
	slug := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(req.Name), " ", "-"))

	updatedCategory, err := h.Repo.UpdateCategory(catID, req.Name, slug, req.Type)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update category"})
		return
	}

	c.JSON(http.StatusOK, updatedCategory)
}
// GenerateSEOImageUrl appends the listing title as a keyword slug to the Cloudinary URL
// Helper: Formats Cloudinary URLs for performance and SEO without causing 404s
func GenerateSEOImageUrl(cloudinaryURL string, title string) string {
	if cloudinaryURL == "" || !strings.Contains(cloudinaryURL, "cloudinary.com") {
		return cloudinaryURL
	}

	// Ensure f_auto,q_auto optimization flags are present
	if !strings.Contains(cloudinaryURL, "f_auto") {
		cloudinaryURL = strings.Replace(
			cloudinaryURL,
			"/image/upload/",
			"/image/upload/f_auto,q_auto,w_1200,c_limit/",
			1,
		)
	}

	return cloudinaryURL
}
// POST /api/vendor/upload
func (h *VendorHandler) UploadImage(c *gin.Context) {
	// 1. Accept either "file" or "image" field keys to prevent 400 errors
	fileHeader, err := c.FormFile("file")
	if err != nil {
		fileHeader, err = c.FormFile("image")
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file uploaded. Form key must be 'file' or 'image'"})
		return
	}

	// 2. Open the file stream
	file, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read uploaded file"})
		return
	}
	defer file.Close()

	// 3. Fallback check if Cloudinary client is initialized
	if h.Cld == nil {
		cldURL := os.Getenv("CLOUDINARY_URL")
		cld, err := cloudinary.NewFromURL(cldURL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Cloudinary storage not configured"})
			return
		}
		h.Cld = cld
	}

	// 4. Upload file stream to Cloudinary
	uploadResult, err := h.Cld.Upload.Upload(c.Request.Context(), file, uploader.UploadParams{
		Folder: "askmtaa/listings", // Organizes files inside Cloudinary dashboard
	})
	if err != nil {
		log.Printf("❌ Cloudinary Upload Error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to upload image to cloud"})
		return
	}

	// 5. Transform raw URL to include automatic SEO & Performance parameters:
	// Converts: /image/upload/v1234/file.png
	// Into:     /image/upload/f_auto,q_auto,w_1200,c_limit/v1234/file.png
	optimizedURL := strings.Replace(
		uploadResult.SecureURL,
		"/image/upload/",
		"/image/upload/f_auto,q_auto,w_1200,c_limit/",
		1,
	)

	// 6. Return both 'url' and 'imageUrl' keys to cover all frontend expectations
	c.JSON(http.StatusOK, gin.H{
		"url":      optimizedURL,
		"imageUrl": optimizedURL,
		"rawUrl":   uploadResult.SecureURL,
	})
}
// GET /api/vendor/profile

// GET /api/admin/vendors
func (h *VendorHandler) GetAllVendors(c *gin.Context) {
    vendors, err := h.Repo.GetAllVendors()
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch vendors"})
        return
    }

    c.JSON(http.StatusOK, vendors)
}
// GET /api/vendor/dashboard
func (h *VendorHandler) GetDashboardData(c *gin.Context) {
    vendor, err := h.getAuthenticatedVendor(c)
    if err != nil {
        c.JSON(http.StatusUnauthorized, gin.H{"error": "Vendor not found"})
        return
    }

    // Extract user role set by your Auth Middleware (e.g., c.Set("role", claims.Role))
    role, _ := c.Get("role")
    userRole, _ := role.(string)

    // Pass vendor.ID and userRole to repository
    stats, err := h.Repo.GetDashboardStats(vendor.ID, userRole)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch dashboard data"})
        return
    }

    c.JSON(http.StatusOK, gin.H{
        "vendor": vendor,
        "stats":  stats,
    })
}
type ForgotPasswordOTPInput struct {
    Email string `json:"email" binding:"required,email"`
}

type ResetPasswordWithOTPInput struct {
    Email       string `json:"email" binding:"required,email"`
    OTP         string `json:"otp" binding:"required,len=6"`
    NewPassword string `json:"new_password" binding:"required,min=6"`
}

func (h *AuthHandler) ForgotPassword(c *gin.Context) {
    var input ForgotPasswordOTPInput
    if err := c.ShouldBindJSON(&input); err != nil {
        c.JSON(400, gin.H{"error": "Please provide a valid email address"})
        return
    }

    _ = h.repo.ForgotPasswordOTP(input.Email)

    c.JSON(200, gin.H{
        "message": "If that email is registered, a 6-digit OTP code has been sent.",
    })
}

func (h *AuthHandler) ResetPassword(c *gin.Context) {
    var input ResetPasswordWithOTPInput
    if err := c.ShouldBindJSON(&input); err != nil {
        c.JSON(400, gin.H{"error": "Invalid input. Code must be 6 digits and password at least 6 characters."})
        return
    }

    if err := h.repo.ResetPasswordWithOTP(input.Email, input.OTP, input.NewPassword); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }

    c.JSON(200, gin.H{"message": "Password successfully reset! You can now log in."})
}

func GetVendorNotifications(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		vendorIDVal, exists := c.Get("vendorID")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized access"})
			return
		}

		vendorIDStr := fmt.Sprintf("%v", vendorIDVal)

		var notifications []models.Notification
		err := db.Where("vendor_id = ?", vendorIDStr).
			Order("created_at desc").
			Find(&notifications).Error

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve notifications"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"notifications": notifications,
		})
	}
}



type UpdateOptInDTO struct {
	WhatsAppOptIn bool `json:"whatsapp_opt_in"`
}
// Helper: Extracts vendor ID safely from Gin context across multiple key conventions
// Helper: Safely extracts vendor ID whether stored as string or uuid.UUID in context
func getVendorIDFromContext(c *gin.Context) string {
	// 1. Check if stored as string
	if id := c.GetString("vendorID"); id != "" && id != "00000000-0000-0000-0000-000000000000" {
		return id
	}

	// 2. Check if stored as uuid.UUID struct
	if val, exists := c.Get("vendorID"); exists {
		if idUUID, ok := val.(uuid.UUID); ok && idUUID != uuid.Nil {
			return idUUID.String()
		}
	}

	// 3. Fallback for snake_case context keys
	if id := c.GetString("vendor_id"); id != "" {
		return id
	}

	return ""
}

// GET /api/vendor/notifications
func (h *VendorHandler) GetNotifications(c *gin.Context) {
	vendorID := getVendorIDFromContext(c)
	if vendorID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: vendor session not found"})
		return
	}

	notifications, err := h.Repo.GetNotificationsByVendorID(vendorID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, notifications)
}

// GET /api/vendor/notifications/unread-count
func (h *VendorHandler) GetUnreadNotificationCount(c *gin.Context) {
	vendorID := getVendorIDFromContext(c)
	if vendorID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: vendor session not found"})
		return
	}

	count, err := h.Repo.GetUnreadCountByVendorID(vendorID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"unread_count": count})
}

// PATCH /api/vendor/notifications/:id/read
func (h *VendorHandler) MarkNotificationRead(c *gin.Context) {
	vendorID := getVendorIDFromContext(c)
	if vendorID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: vendor session not found"})
		return
	}

	notifID := c.Param("id")
	if err := h.Repo.MarkAsRead(notifID, vendorID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Notification marked as read"})
}

// PATCH /api/vendor/notifications/read-all
func (h *VendorHandler) MarkAllNotificationsRead(c *gin.Context) {
	vendorID := getVendorIDFromContext(c)
	if vendorID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: vendor session not found"})
		return
	}

	if err := h.Repo.MarkAllAsRead(vendorID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "All notifications marked as read"})
}
// PATCH /api/vendor/settings/whatsapp-optin
func (h *VendorHandler) ToggleWhatsAppOptIn(c *gin.Context) {
	vendorID := c.GetString("vendorID")

	var dto UpdateOptInDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
		return
	}

	if err := h.Repo.UpdateWhatsAppOptIn(vendorID, dto.WhatsAppOptIn); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":         "WhatsApp opt-in updated successfully",
		"whatsapp_opt_in": dto.WhatsAppOptIn,
	})
}