package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port                    string
	FaceSearchToken         string
	DatabaseURL             string
	AWSRegion               string
	AWSAccessKey            string
	AWSSecretKey            string
	S3Bucket                string
	RekognitionCollection   string
	FaceSearchThreshold     float64
	MaxFaces                int32
	VerificationEnabled      bool
	VerificationThresholdHi  float64
	VerificationThresholdLo  float64
	VerificationMaxFaces     int32
}

func Load() *Config {
	return &Config{
		Port:                    getEnv("PORT", "8080"),
		FaceSearchToken:         getEnv("FACE_SEARCH_TOKEN", ""),
		DatabaseURL:             getEnv("DATABASE_URL", "postgres://localhost:5432/perfilamiento"),
		AWSRegion:               getEnv("AWS_REGION", "us-east-1"),
		AWSAccessKey:            getEnv("AWS_ACCESS_KEY_ID", ""),
		AWSSecretKey:            getEnv("AWS_SECRET_ACCESS_KEY", ""),
		S3Bucket:                getEnv("AWS_S3_BUCKET_NAME", "perfilamiento-faces"),
		RekognitionCollection:   getEnv("REKOGNITION_COLLECTION_ID", "socios_stadium_users"),
		FaceSearchThreshold:     getEnvFloat("FACE_SEARCH_THRESHOLD", 80),
		MaxFaces:                int32(getEnvInt("MAX_FACES", 20)),
		VerificationEnabled:      getEnvBool("FACE_VERIFICATION_ENABLED", false),
		VerificationThresholdHi:  getEnvFloat("FACE_VERIFICATION_THRESHOLD_HI", 88),
		VerificationThresholdLo:  getEnvFloat("FACE_VERIFICATION_THRESHOLD_LO", 80),
		VerificationMaxFaces:     int32(getEnvInt("FACE_VERIFICATION_MAX_FACES", 5)),
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

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatVal, err := strconv.ParseFloat(value, 64); err == nil {
			return floatVal
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return value == "true" || value == "1"
	}
	return defaultValue
}
