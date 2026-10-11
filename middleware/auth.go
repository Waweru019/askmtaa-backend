package middleware

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// GetJWTSecret retrieves secret key from ENV or defaults to fallback
func GetJWTSecret() []byte {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		return []byte("your_super_secret_jwt_key_change_in_env")
	}
	return []byte(secret)
}

// Custom Claims Structure
type Claims struct {
	UserID   uuid.UUID `json:"userId"`
	VendorID uuid.UUID `json:"vendorId,omitempty"`
	Role     string    `json:"role"`
	jwt.RegisteredClaims
}

// -----------------------------------------------------------------------------
// Helper Functions (Token Generation & Validation)
// -----------------------------------------------------------------------------

// GenerateJWT creates a new token valid for 24 hours
func GenerateJWT(userID, vendorID uuid.UUID, role string) (string, error) {
	claims := Claims{
		UserID:   userID,
		VendorID: vendorID,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(GetJWTSecret())
}

// ValidateJWT verifies the signature and parses claims from a token string
func ValidateJWT(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return GetJWTSecret(), nil
	})

	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid or expired token")
	}

	return claims, nil
}

// -----------------------------------------------------------------------------
// Gin HTTP Middleware
// -----------------------------------------------------------------------------

// AuthMiddleware inspects the Authorization header and attaches claims to Gin context
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header required"})
			c.Abort()
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header format must be: Bearer <token>"})
			c.Abort()
			return
		}

		claims, err := ValidateJWT(parts[1])
		if err != nil {
			// Returns exact JWT parsing error (e.g., signature mismatch, expired)
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			c.Abort()
			return
		}

		// Keys aligned with VendorHandler (c.Get("userID"))
		c.Set("userID", claims.UserID)
		c.Set("vendorID", claims.VendorID)
		c.Set("role", claims.Role)

		c.Next()
	}
}