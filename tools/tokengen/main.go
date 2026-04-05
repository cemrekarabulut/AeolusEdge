// tokengen — geliştirme ve test için JWT token üretici CLI.
//
// Kullanım:
//   JWT_SECRET=supersecret go run ./tools/tokengen --device turbine-001 --ttl 24h
//
// Production'da bu tool kullanılmaz — cihazlar kayıt sırasında token alır.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"aeolus-edge/internal/infrastructure/auth"
)

func main() {
	deviceID := flag.String("device", "", "Cihaz ID (örn: turbine-001) [zorunlu]")
	ttlStr := flag.String("ttl", "24h", "Token geçerlilik süresi (örn: 1h, 24h, 7d)")
	flag.Parse()

	if *deviceID == "" {
		fmt.Fprintln(os.Stderr, "hata: --device bayrağı zorunlu")
		flag.Usage()
		os.Exit(1)
	}

	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		fmt.Fprintln(os.Stderr, "hata: JWT_SECRET env var ayarlanmamış")
		os.Exit(1)
	}

	ttl, err := time.ParseDuration(*ttlStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hata: geçersiz ttl değeri %q: %v\n", *ttlStr, err)
		os.Exit(1)
	}

	token, err := auth.GenerateToken(*deviceID, secret, ttl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hata: token üretilemedi: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Device  : %s\n", *deviceID)
	fmt.Printf("TTL     : %s\n", ttl)
	fmt.Printf("Token   : %s\n", token)
	fmt.Printf("\nKullanım:\n")
	fmt.Printf("  curl -X POST http://localhost:8080/ingest \\\n")
	fmt.Printf("    -H 'Authorization: Bearer %s' \\\n", token)
	fmt.Printf("    -H 'Content-Type: application/json' \\\n")
	fmt.Printf("    -d '{\"vibration\":4.2,\"rpm\":1800,\"temperature\":72.5}'\n")
}
