package repository

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"main/email"
	"main/models"
)

type VendorProfile struct {
	ID              uuid.UUID `json:"id"`
	UserID          uuid.UUID `json:"userId"`
	BusinessName    string    `json:"businessName"`
	PhoneNumber     string    `json:"phoneNumber"`
	WhatsAppNumber  string    `json:"whatsAppNumber"`
	DefaultLocation string    `json:"defaultLocation"`
	Verified        bool      `json:"verified"`
	CreatedAt       string    `json:"createdAt"`
}

type RegisterInput struct {
	PhoneNumber string `json:"phoneNumber" binding:"required"`
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=6"`
}

type LoginInput struct {
	Identifier string `json:"identifier" binding:"required"`
	Password   string `json:"password" binding:"required"`
}

type AuthRepository interface {
	RegisterVendor(input RegisterInput) (*models.User, *models.Vendor, error)
	Login(input LoginInput) (*models.User, error)
	FindByEmail(email string) (*models.User, error)
	FindByPhone(phone string) (*models.User, error)
	ForgotPasswordOTP(email string) error
	ResetPasswordWithOTP(email string, otp string, newPassword string) error
}

type DashboardStats struct {
	TotalListings  int64 `json:"totalListings"`
	ActiveListings int64 `json:"activeListings"`
	PausedListings int64 `json:"pausedListings"`
}

type authRepository struct {
	db *gorm.DB
}

func NewAuthRepository(db *gorm.DB) AuthRepository {
	return &authRepository{db: db}
}

func (r *authRepository) RegisterVendor(input RegisterInput) (*models.User, *models.Vendor, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, nil, err
	}

	user := models.User{
		PhoneNumber:  input.PhoneNumber,
		Email:        input.Email,
		PasswordHash: string(hashedPassword),
		Role:         "vendor",
	}

	var vendor models.Vendor

	err = r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&user).Error; err != nil {
			return err
		}

		vendor = models.Vendor{
			UserID:   user.ID,
			Verified: false,
		}

		if err := tx.Create(&vendor).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	user.Vendor = vendor

	go func() {
		err := email.SendWelcomeEmail(user.Email, user.PhoneNumber)
		if err != nil {
			log.Printf("Failed to send welcome email to %s: %v", user.Email, err)
		}
	}()

	return &user, &vendor, nil
}

func (r *authRepository) Login(input LoginInput) (*models.User, error) {
	var user models.User

	err := r.db.Preload("Vendor").
		Where("email = ? OR phone_number = ?", input.Identifier, input.Identifier).
		First(&user).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("invalid email/phone number or password")
		}
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)); err != nil {
		return nil, errors.New("invalid email/phone number or password")
	}

	return &user, nil
}

func (r *authRepository) FindByEmail(email string) (*models.User, error) {
	var user models.User
	err := r.db.Where("email = ?", email).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (r *authRepository) FindByPhone(phone string) (*models.User, error) {
	var user models.User
	err := r.db.Where("phone_number = ?", phone).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

type VendorRepository interface {
	// Vendor operations
	GetFirstVendor() (*models.Vendor, error)
	GetVendorByID(id uuid.UUID) (*models.Vendor, error)
	GetVendorByUserID(userID uuid.UUID) (*models.Vendor, error)
	UpdateVendor(vendor *models.Vendor) error
	GetAllVendors() ([]VendorProfile, error)

	// Listing operations
	GetDashboardStats(vendorID uuid.UUID, role string) (*DashboardStats, error)
	GetListingsByVendor(vendorID uuid.UUID, role string) ([]models.Listing, error)
	GetListingByID(id uuid.UUID) (*models.Listing, error)
	GetPublicListings() ([]models.Listing, error)
	CreateListing(listing *models.Listing, subServices []models.SubService) (*models.Listing, error)
	UpdateListingStatus(id uuid.UUID, status string) error
	DeleteListing(id uuid.UUID) error
	UpdateListing(listing *models.Listing, subServices []models.SubService) (*models.Listing, error)

	// Category operations
	GetCategories() ([]models.Category, error)
	CreateCategory(category *AdminCategory) error
	DeleteCategory(id string) error
	UpdateCategory(id string, name string, slug string, categoryType string) (*AdminCategory, error)

	// Notification & WhatsApp Opt-In operations
	GetNotificationsByVendorID(vendorID string) ([]models.Notification, error)
	GetUnreadCountByVendorID(vendorID string) (int64, error)
	MarkAsRead(notificationID string, vendorID string) error
	MarkAllAsRead(vendorID string) error
	CreateNotification(notification *models.Notification) error
	UpdateWhatsAppOptIn(vendorID string, optIn bool) error
	IsVendorOptedInToWhatsApp(vendorID string) (bool, error)
}

type vendorRepository struct {
	db *gorm.DB
}

func NewVendorRepository(db *gorm.DB) VendorRepository {
	return &vendorRepository{db: db}
}

func (r *vendorRepository) GetFirstVendor() (*models.Vendor, error) {
	var vendor models.Vendor
	if err := r.db.First(&vendor).Error; err != nil {
		return nil, err
	}
	return &vendor, nil
}

func (r *vendorRepository) GetVendorByID(id uuid.UUID) (*models.Vendor, error) {
	var vendor models.Vendor
	if err := r.db.First(&vendor, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &vendor, nil
}

func (r *vendorRepository) GetVendorByUserID(userID uuid.UUID) (*models.Vendor, error) {
	var vendor models.Vendor
	if err := r.db.First(&vendor, "user_id = ?", userID).Error; err != nil {
		return nil, err
	}
	return &vendor, nil
}

func (r *vendorRepository) UpdateVendor(vendor *models.Vendor) error {
	return r.db.Save(vendor).Error
}

func (r *vendorRepository) GetListingsByVendor(vendorID uuid.UUID, role string) ([]models.Listing, error) {
	var listings []models.Listing

	query := r.db.Preload("SubServices").
		Preload("Category").
		Order("created_at desc")

	if role != "admin" {
		query = query.Where("vendor_id = ?", vendorID)
	}

	err := query.Find(&listings).Error
	return listings, err
}

func (r *vendorRepository) GetPublicListings() ([]models.Listing, error) {
	var listings []models.Listing
	err := r.db.Preload("SubServices").
		Preload("Category").
		Where("status = ?", "active").
		Order("created_at desc").
		Find(&listings).Error

	return listings, err
}

func (r *vendorRepository) GetListingByID(id uuid.UUID) (*models.Listing, error) {
	var listing models.Listing
	err := r.db.Preload("SubServices").
		Preload("Category").
		First(&listing, "id = ?", id).Error

	if err != nil {
		return nil, err
	}
	return &listing, nil
}

func (r *vendorRepository) CreateListing(listing *models.Listing, subServices []models.SubService) (*models.Listing, error) {
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(listing).Error; err != nil {
			return err
		}

		if listing.ListingType == "service" && len(subServices) > 0 {
			for i := range subServices {
				if subServices[i].Name != "" {
					subServices[i].ListingID = listing.ID
					if err := tx.Create(&subServices[i]).Error; err != nil {
						return err
					}
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return r.GetListingByID(listing.ID)
}

func (r *vendorRepository) UpdateListingStatus(id uuid.UUID, status string) error {
	return r.db.Model(&models.Listing{}).Where("id = ?", id).Update("status", status).Error
}

func (r *vendorRepository) UpdateListing(listing *models.Listing, subServices []models.SubService) (*models.Listing, error) {
	tx := r.db.Begin()

	if err := tx.Model(&models.Listing{}).Where("id = ? AND vendor_id = ?", listing.ID, listing.VendorID).Updates(map[string]interface{}{
		"category_id":      listing.CategoryID,
		"title":            listing.Title,
		"description":      listing.Description,
		"listing_type":     listing.ListingType,
		"price":            listing.Price,
		"price_type":       listing.PriceType,
		"image_url":        listing.ImageURL,
		"location":         listing.Location,
		"exact_address":    listing.ExactAddress,
		"latitude":         listing.Latitude,
		"longitude":        listing.Longitude,
		"start_date":       listing.StartDate,
		"end_date":         listing.EndDate,
		"meeting_point":    listing.MeetingPoint,
		"network_provider": listing.NetworkProvider,
		"instructions":     listing.Instructions,
		"status":           listing.Status,
	}).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	if err := tx.Where("listing_id = ?", listing.ID).Delete(&models.SubService{}).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	if len(subServices) > 0 {
		for i := range subServices {
			subServices[i].ID = uuid.New()
			subServices[i].ListingID = listing.ID
		}
		if err := tx.Create(&subServices).Error; err != nil {
			tx.Rollback()
			return nil, err
		}
	}

	if err := tx.Commit().Error; err != nil {
		return nil, err
	}

	var updated models.Listing
	if err := r.db.Preload("SubServices").First(&updated, "id = ?", listing.ID).Error; err != nil {
		return listing, nil
	}

	return &updated, nil
}

func (r *vendorRepository) DeleteListing(id uuid.UUID) error {
	return r.db.Where("id = ?", id).Delete(&models.Listing{}).Error
}

func (r *vendorRepository) GetCategories() ([]models.Category, error) {
	var categories []models.Category
	err := r.db.Order("name asc").Find(&categories).Error
	return categories, err
}

func (r *vendorRepository) DeleteCategory(id string) error {
	var count int64
	if err := r.db.Table("listings").Where("category_id = ?", id).Count(&count).Error; err == nil && count > 0 {
		return errors.New("cannot delete category because active listings are assigned to it")
	}

	result := r.db.Table("categories").Where("id = ?", id).Delete(&AdminCategory{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("category not found")
	}
	return nil
}

func (r *vendorRepository) CreateCategory(category *AdminCategory) error {
	return r.db.Unscoped().Table("categories").Create(category).Error
}

func (r *vendorRepository) UpdateCategory(id string, name string, slug string, categoryType string) (*AdminCategory, error) {
	var category AdminCategory

	if err := r.db.First(&category, "id = ?", id).Error; err != nil {
		return nil, err
	}

	category.Name = name
	category.Slug = slug
	category.Type = categoryType

	if err := r.db.Save(&category).Error; err != nil {
		return nil, err
	}

	return &category, nil
}

type AdminCategory struct {
	ID   string `gorm:"primaryKey;type:uuid" json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	Type string `json:"type"`
}

func (AdminCategory) TableName() string {
	return "categories"
}

func (r *vendorRepository) GetDashboardStats(vendorID uuid.UUID, role string) (*DashboardStats, error) {
	var stats DashboardStats

	totalQuery := r.db.Model(&models.Listing{})
	activeQuery := r.db.Model(&models.Listing{}).Where("status = ?", "active")
	pausedQuery := r.db.Model(&models.Listing{}).Where("status = ?", "paused")

	if role != "admin" {
		totalQuery = totalQuery.Where("vendor_id = ?", vendorID)
		activeQuery = activeQuery.Where("vendor_id = ?", vendorID)
		pausedQuery = pausedQuery.Where("vendor_id = ?", vendorID)
	}

	if err := totalQuery.Count(&stats.TotalListings).Error; err != nil {
		return nil, err
	}

	if err := activeQuery.Count(&stats.ActiveListings).Error; err != nil {
		return nil, err
	}

	if err := pausedQuery.Count(&stats.PausedListings).Error; err != nil {
		return nil, err
	}

	return &stats, nil
}

func (r *vendorRepository) GetAllVendors() ([]VendorProfile, error) {
	type VendorQueryResult struct {
		ID              uuid.UUID `gorm:"column:id"`
		UserID          uuid.UUID `gorm:"column:user_id"`
		BusinessName    string    `gorm:"column:business_name"`
		PhoneNumber     string    `gorm:"column:phone_number"`
		WhatsAppNumber  string    `gorm:"column:whats_app_number"`
		DefaultLocation string    `gorm:"column:default_location"`
		Verified        bool      `gorm:"column:verified"`
		CreatedAt       time.Time `gorm:"column:created_at"`
	}

	var results []VendorQueryResult

	err := r.db.Table("vendors").
		Select(`
            vendors.id, 
            vendors.user_id, 
            vendors.business_name, 
            COALESCE(NULLIF(vendors.phone_number, ''), users.phone_number) AS phone_number, 
            vendors.whats_app_number, 
            vendors.default_location, 
            vendors.verified, 
            vendors.created_at
        `).
		Joins("JOIN users ON users.id = vendors.user_id").
		Order("vendors.created_at DESC").
		Scan(&results).Error

	if err != nil {
		return nil, err
	}

	profiles := make([]VendorProfile, len(results))
	for i, res := range results {
		profiles[i] = VendorProfile{
			ID:              res.ID,
			UserID:          res.UserID,
			BusinessName:    res.BusinessName,
			PhoneNumber:     res.PhoneNumber,
			WhatsAppNumber:  res.WhatsAppNumber,
			DefaultLocation: res.DefaultLocation,
			Verified:        res.Verified,
			CreatedAt:       res.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
	}

	return profiles, nil
}

func generateOTP() string {
	var table = [...]byte{'1', '2', '3', '4', '5', '6', '7', '8', '9', '0'}
	b := make([]byte, 6)
	n, err := io.ReadAtLeast(rand.Reader, b, 6)
	if n != 6 || err != nil {
		return "123456"
	}
	for i := 0; i < len(b); i++ {
		b[i] = table[int(b[i])%len(table)]
	}
	return string(b)
}

func (r *authRepository) ForgotPasswordOTP(emailInput string) error {
	var user models.User
	if err := r.db.Where("email = ?", emailInput).First(&user).Error; err != nil {
		return nil
	}

	otp := generateOTP()
	user.PasswordResetOTP = otp
	user.PasswordResetExpiresAt = time.Now().Add(10 * time.Minute)

	if err := r.db.Save(&user).Error; err != nil {
		return err
	}

	go func() {
		if err := email.SendPasswordResetOTP(user.Email, otp); err != nil {
			log.Printf("Failed to send OTP email: %v", err)
		}
	}()

	return nil
}

func (r *authRepository) ResetPasswordWithOTP(emailInput string, otp string, newPassword string) error {
	var user models.User

	err := r.db.Where("email = ? AND password_reset_otp = ? AND password_reset_expires_at > ?", emailInput, otp, time.Now()).First(&user).Error
	if err != nil {
		return errors.New("invalid or expired OTP code")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	return r.db.Model(&user).Updates(map[string]interface{}{
		"password_hash":             string(hashedPassword),
		"password_reset_otp":        "",
		"password_reset_expires_at": nil,
	}).Error
}

// =========================================================================
// 🔔 NOTIFICATION & OPT-IN REPOSITORY METHODS
// =========================================================================

func (r *vendorRepository) GetNotificationsByVendorID(vendorID string) ([]models.Notification, error) {
	var notifications []models.Notification
	err := r.db.Where("vendor_id = ?", vendorID).
		Order("created_at desc").
		Find(&notifications).Error
	if err != nil {
		return nil, fmt.Errorf("failed to fetch notifications: %w", err)
	}
	return notifications, nil
}

func (r *vendorRepository) GetUnreadCountByVendorID(vendorID string) (int64, error) {
	var count int64
	err := r.db.Model(&models.Notification{}).
		Where("vendor_id = ? AND is_read = ?", vendorID, false).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("failed to count unread notifications: %w", err)
	}
	return count, nil
}

func (r *vendorRepository) MarkAsRead(notificationID string, vendorID string) error {
	result := r.db.Model(&models.Notification{}).
		Where("id = ? AND vendor_id = ?", notificationID, vendorID).
		Update("is_read", true)

	if result.Error != nil {
		return fmt.Errorf("failed to mark notification as read: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("notification not found or access denied")
	}
	return nil
}

func (r *vendorRepository) MarkAllAsRead(vendorID string) error {
	err := r.db.Model(&models.Notification{}).
		Where("vendor_id = ? AND is_read = ?", vendorID, false).
		Update("is_read", true).Error
	if err != nil {
		return fmt.Errorf("failed to mark all notifications as read: %w", err)
	}
	return nil
}

func (r *vendorRepository) CreateNotification(notification *models.Notification) error {
	if err := r.db.Create(notification).Error; err != nil {
		return fmt.Errorf("failed to create notification: %w", err)
	}
	return nil
}

func (r *vendorRepository) UpdateWhatsAppOptIn(vendorID string, optIn bool) error {
	err := r.db.Model(&models.Vendor{}).
		Where("id = ?", vendorID).
		Update("whats_app_opt_in", optIn).Error
	if err != nil {
		return fmt.Errorf("failed to update WhatsApp opt-in setting: %w", err)
	}
	return nil
}

func (r *vendorRepository) IsVendorOptedInToWhatsApp(vendorID string) (bool, error) {
	var vendor models.Vendor
	err := r.db.Select("whats_app_opt_in").
		Where("id = ?", vendorID).
		First(&vendor).Error
	if err != nil {
		return false, fmt.Errorf("failed to fetch vendor opt-in status: %w", err)
	}
	return vendor.WhatsAppOptIn, nil
}