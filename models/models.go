package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type User struct {
	ID           uuid.UUID      `gorm:"type:char(36);primaryKey" json:"id"`
	PhoneNumber  string         `gorm:"type:varchar(30);uniqueIndex;not null" json:"phoneNumber"`
	Email        string         `gorm:"type:varchar(150);uniqueIndex;not null" json:"email"`
	PasswordHash string         `gorm:"not null" json:"-"`
	Role         string         `gorm:"not null;default:'vendor'" json:"role"` // 'admin', 'vendor', 'customer'
	PasswordResetOTP       string    `gorm:"default:null"`
    PasswordResetExpiresAt time.Time `gorm:"default:null"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`

	Vendor Vendor `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"vendor,omitempty"`
}

func (u *User) BeforeCreate(tx *gorm.DB) (err error) {
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	return
}

type Notification struct {
	ID        string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	VendorID  string    `gorm:"index;type:varchar(36)" json:"vendor_id"`
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	Type      string    `gorm:"default:'lead'" json:"type"` // 👈 Add this line
	IsRead    bool      `gorm:"default:false" json:"is_read"`
	CreatedAt time.Time `json:"created_at"`
}
type Vendor struct {
	ID              uuid.UUID `gorm:"type:char(36);primaryKey" json:"id"`
	UserID          uuid.UUID `gorm:"type:char(36);not null;index" json:"userId"`
	BusinessName    string    `gorm:"type:varchar(100)" json:"businessName"`
	PhoneNumber     string    `gorm:"type:varchar(20)" json:"phoneNumber"`
	WhatsAppNumber  string    `gorm:"type:varchar(20)" json:"whatsAppNumber"`
	DefaultLocation string    `gorm:"type:varchar(100)" json:"defaultLocation"` // e.g. "Embu, Kenya"
	ExactAddress    string    `gorm:"type:varchar(255)" json:"exactAddress"`    // e.g. "Kenyatta Highway, Near Shell Petrol Station"
	Latitude        *float64  `gorm:"type:decimal(10,8)" json:"latitude"`       // e.g. -0.5386
	Longitude       *float64  `gorm:"type:decimal(11,8)" json:"longitude"`      // e.g. 37.4589
	Verified        bool      `gorm:"default:false" json:"verified"`
	WhatsAppOptIn   bool      `gorm:"default:false" json:"whatsAppOptedIn"`
    NotifyWhatsApp    bool      `gorm:"default:true" json:"notifyWhatsApp"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

func (v *Vendor) BeforeCreate(tx *gorm.DB) (err error) {
	if v.ID == uuid.Nil {
		v.ID = uuid.New()
	}
	return
}

type Category struct {
	ID   uuid.UUID `gorm:"type:char(36);primaryKey" json:"id"`
	Name string    `gorm:"type:varchar(100)" json:"name"`
	Slug string    `gorm:"type:varchar(100)" json:"slug"`
	Type string    `gorm:"type:varchar(50)" json:"type"`
}

func (c *Category) BeforeCreate(tx *gorm.DB) (err error) {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	return
}

type SubService struct {
	ID        uuid.UUID `gorm:"type:char(36);primaryKey" json:"id"`
	ListingID uuid.UUID `gorm:"type:char(36);not null;index" json:"listingId"`
	Name      string    `gorm:"type:varchar(100);not null" json:"name"`
	Price     float64   `gorm:"type:decimal(10,2);not null" json:"price"`
}

func (s *SubService) BeforeCreate(tx *gorm.DB) (err error) {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	return
}
type Listing struct {
	ID          uuid.UUID `gorm:"type:char(36);primaryKey" json:"id"`
	VendorID    uuid.UUID `gorm:"type:char(36);not null;index" json:"vendorId"`
	Vendor      *Vendor   `gorm:"foreignKey:VendorID" json:"vendor,omitempty"`
	CategoryID  uuid.UUID `gorm:"type:char(36);not null" json:"categoryId"`
	Category    *Category `gorm:"foreignKey:CategoryID" json:"category,omitempty"`
	Title       string    `gorm:"type:varchar(255);not null" json:"title"`
	Description string    `gorm:"type:text" json:"description"`
	ListingType string    `gorm:"type:varchar(20);not null" json:"listingType"`
	Status      string    `gorm:"type:varchar(20);default:'active'" json:"status"`
	Price       float64   `gorm:"type:decimal(10,2);not null" json:"price"`
	PriceType   string    `gorm:"type:varchar(20)" json:"priceType"`
	ImageURL    string    `gorm:"type:text" json:"imageUrl"`
	Location    string    `gorm:"type:varchar(100);not null" json:"location"`

	// --- Location Details ---
	ExactAddress string   `gorm:"type:varchar(255)" json:"exactAddress"`
	Latitude     *float64 `gorm:"type:decimal(10,8)" json:"latitude"`
	Longitude    *float64 `gorm:"type:decimal(11,8)" json:"longitude"`

	// --- Trip & Event Details ---
	StartDate    *time.Time `json:"startDate,omitempty"`
	EndDate      *time.Time `json:"endDate,omitempty"`
	MeetingPoint string     `gorm:"type:varchar(255)" json:"meetingPoint,omitempty"`

	// --- Data Mwitu & Digital Details ---
	NetworkProvider string `gorm:"type:varchar(50)" json:"networkProvider,omitempty"`
	Instructions    string `gorm:"type:text" json:"instructions,omitempty"`

	SubServices []SubService `gorm:"foreignKey:ListingID;constraint:OnDelete:CASCADE" json:"subServices,omitempty"`
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`
}

func (l *Listing) BeforeCreate(tx *gorm.DB) (err error) {
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	return
}