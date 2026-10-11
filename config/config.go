package config

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

// getEnv fetches environment variables with a fallback default
func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// ConnectDatabase sets up the connection pool for TiDB MySQL using environment variables
func ConnectDatabase() {
	// 1. Load .env file for local development (silently ignored in production/cloud hosts)
	if err := godotenv.Load(); err != nil {
		log.Println("ℹ️ No .env file found; falling back to system environment variables")
	}

	host := getEnv("DB_HOST", "gateway01.ap-northeast-1.prod.aws.tidbcloud.com")
	user := getEnv("DB_USER", "QZ8eqTVmvraAqiq.root")
	password := os.Getenv("DB_PASSWORD")
	dbname := getEnv("DB_NAME", "askmtaa")
	port := getEnv("DB_PORT", "4000")

	if password == "" {
		log.Fatal("❌ DB_PASSWORD environment variable is not set!")
	}

	// 2. Connect to default 'test' database to ensure 'askmtaa' exists
	initDSN := fmt.Sprintf("%s:%s@tcp(%s:%s)/test?charset=utf8mb4&parseTime=True&loc=Local&tls=true",
		user, password, host, port)

	rawDB, err := sql.Open("mysql", initDSN)
	if err != nil {
		log.Fatalf("❌ Failed to reach TiDB cluster: %v", err)
	}
	defer rawDB.Close()

	_, err = rawDB.Exec(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`;", dbname))
	if err != nil {
		log.Fatalf("❌ Failed to create database '%s': %v", dbname, err)
	}

	// 3. Connect GORM to the 'askmtaa' database
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local&tls=true",
		user, password, host, port, dbname)

	database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		log.Fatalf("❌ Failed to connect to database '%s': %v", dbname, err)
	}

	fmt.Println("🚀 MySQL/TiDB Database connection successfully established!")
	DB = database
}