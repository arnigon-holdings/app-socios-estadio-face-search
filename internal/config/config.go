package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port            string
	FaceSearchToken string
	DatabaseURL     string
	AWSRegion       string
	AWSAccessKey    string
	AWSSecretKey    string
	S3Bucket        string
	RekognitionCollection string
}

func Load() *Config {
	return &Config{
		Port:               getEnv("PORT", "8080"),
		FaceSearchToken:    getEnv("FACE_SEARCH_TOKEN", ""),
		DatabaseURL:        getEnv("DATABASE_URL", "postgres://localhost:5432/perfilamiento"),
		AWSRegion:          getEnv("AWS_REGION", "us-east-1"),
		AWSAccessKey:       getEnv("AWS_ACCESS_KEY_ID", ""),
		AWSSecretKey:       getEnv("AWS_SECRET_ACCESS_KEY", ""),
		S3Bucket:           getEnv("AWS_S3_BUCKET_NAME", "perfilamiento-faces"),
		RekognitionCollection: getEnv("REKOGNITION_COLLECTION_ID", "socios_stadium_users"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}
