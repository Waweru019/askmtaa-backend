package services

import (
	"context"
	"fmt"
	"mime/multipart"
	"os"

	"github.com/cloudinary/cloudinary-go/v2"
	"github.com/cloudinary/cloudinary-go/v2/api/uploader"
)

type CloudinaryService struct {
	cld *cloudinary.Cloudinary
}

// NewCloudinaryService initializes the Cloudinary client
func NewCloudinaryService() (*CloudinaryService, error) {
	cldURL := os.Getenv("CLOUDINARY_URL")
	if cldURL == "" {
		return nil, fmt.Errorf("CLOUDINARY_URL environment variable is not set")
	}

	cld, err := cloudinary.NewFromURL(cldURL)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Cloudinary: %w", err)
	}

	return &CloudinaryService{cld: cld}, nil
}

// UploadImage uploads a file stream to Cloudinary and returns the HTTPS secure URL
func (s *CloudinaryService) UploadImage(ctx context.Context, file multipart.File, folderName string) (string, error) {
	defer file.Close()

	uploadResult, err := s.cld.Upload.Upload(ctx, file, uploader.UploadParams{
		Folder: folderName, // e.g. "askmtaa/listings"
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload image to Cloudinary: %w", err)
	}

	return uploadResult.SecureURL, nil
}