package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/necly/services/internal/config"
)

func TestAPISecurityHeadersAllowCrossOriginFrontendResources(t *testing.T) {
	app := fiber.New()
	app.Use(apiSecurityHeaders())
	app.Get("/catalog", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) })

	req, err := http.NewRequest(http.MethodGet, "/catalog", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if got := response.Header.Get("Cross-Origin-Resource-Policy"); got != "cross-origin" {
		t.Fatalf("Cross-Origin-Resource-Policy = %q, want cross-origin", got)
	}
}

func TestAPICORSAllowsFrontendCheckoutHeaders(t *testing.T) {
	app := fiber.New()
	app.Use(apiCORS(config.CORSConfig{
		Origins:          []string{"https://store.necly.test"},
		AllowCredentials: false,
	}))
	app.Post("/checkout", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })

	req, err := http.NewRequest(http.MethodOptions, "/checkout", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://store.necly.test")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type,idempotency-key")
	response, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "https://store.necly.test" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := response.Header.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want absent", got)
	}
	allowedHeaders := strings.ToLower(response.Header.Get("Access-Control-Allow-Headers"))
	if !strings.Contains(allowedHeaders, "idempotency-key") {
		t.Fatalf("Access-Control-Allow-Headers = %q, missing Idempotency-Key", allowedHeaders)
	}
}
