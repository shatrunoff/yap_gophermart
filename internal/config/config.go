package config

import (
	"flag"
	"os"
)

// Config — конфигурация сервиса.
type Config struct {
	RunAddress     string
	DatabaseURI    string
	AccrualAddress string
	SecretKey      string
}

// Load читает конфигурацию из флагов и/или переменных окружения.
// Флаги имеют приоритет над переменными окружения.
func Load() Config {
	var cfg Config

	// Значения по умолчанию
	cfg.RunAddress = "localhost:8081"
	cfg.AccrualAddress = os.Getenv("ACCRUAL_SYSTEM_ADDRESS")
	cfg.DatabaseURI = os.Getenv("DATABASE_URI")
	cfg.SecretKey = os.Getenv("SECRET_KEY")

	runAddr := flag.String("a", os.Getenv("RUN_ADDRESS"), "адрес и порт запуска сервиса")
	dbURI := flag.String("d", os.Getenv("DATABASE_URI"), "адрес подключения к базе данных")
	accrual := flag.String("r", os.Getenv("ACCRUAL_SYSTEM_ADDRESS"), "адрес системы расчёта начислений")
	secret := flag.String("s", os.Getenv("SECRET_KEY"), "секрет для подписи токенов (опционально)")

	flag.Parse()

	if *runAddr != "" {
		cfg.RunAddress = *runAddr
	}
	if *dbURI != "" {
		cfg.DatabaseURI = *dbURI
	}
	if *accrual != "" {
		cfg.AccrualAddress = *accrual
	}
	if *secret != "" {
		cfg.SecretKey = *secret
	}

	return cfg
}
