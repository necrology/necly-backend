package service

import (
	"errors"
	"testing"
)

func TestCanonicalCheckoutFingerprintMatchesW0Fixture(t *testing.T) {
	items := []CheckoutItemInput{{
		ProductPriceID: "0192F0A4-0000-0000-0000-000000000001",
		Quantity:       1,
		DeviceCount:    1,
	}}
	if got, want := CanonicalCheckoutFingerprint(items, " welcome10 ", ""), "1be64cc17d5ed1305d7ab2f0c0cc53307212d2fe8f275c3600a00e281b699528"; got != want {
		t.Fatalf("fingerprint = %s, want %s", got, want)
	}
	if got, want := CanonicalCheckoutFingerprint(items, "", ""), "46a15f936bb8eb4199e7a6879736f18a3e634d620eb82cc58cf5d114c231e9cf"; got != want {
		t.Fatalf("no-voucher fingerprint = %s, want %s", got, want)
	}
}

func TestCanonicalCheckoutFingerprintSortsItems(t *testing.T) {
	first := []CheckoutItemInput{
		{ProductPriceID: "0192f0a4-0000-0000-0000-000000000002", Quantity: 1, DeviceCount: 2},
		{ProductPriceID: "0192f0a4-0000-0000-0000-000000000001", Quantity: 2, DeviceCount: 1},
	}
	second := []CheckoutItemInput{first[1], first[0]}
	if CanonicalCheckoutFingerprint(first, "", "") != CanonicalCheckoutFingerprint(second, "", "") {
		t.Fatal("fingerprint depends on client item order")
	}
}

func TestCanonicalCheckoutFingerprintIncludesChannel(t *testing.T) {
	items := []CheckoutItemInput{{
		ProductPriceID: "0192f0a4-0000-0000-0000-000000000001",
		Quantity:       1,
		DeviceCount:    1,
	}}
	if got, want := CanonicalCheckoutFingerprint(items, "WELCOME10", "qris"), "1326a11a1c9957fa7b9b74822ead2f72731c4abfd7c79d2d8f81693d7c88b7c6"; got != want {
		t.Fatalf("qris fingerprint = %s, want %s", got, want)
	}
	if CanonicalCheckoutFingerprint(items, "WELCOME10", "qris") == CanonicalCheckoutFingerprint(items, "WELCOME10", "ovo") {
		t.Fatal("fingerprint does not distinguish payment channels")
	}
}

func TestNormalisePaymentChannel(t *testing.T) {
	for input, want := range map[string]string{
		"":           "",
		"  QRIS  ":   "qris",
		"bni_va":     "bni_va",
		"Mandiri_VA": "mandiri_va",
	} {
		got, err := NormalisePaymentChannel(input)
		if err != nil || got != want {
			t.Fatalf("NormalisePaymentChannel(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"bitcoin", "credit_card", "bca_va"} {
		if _, err := NormalisePaymentChannel(input); !errors.Is(err, ErrUnsupportedPaymentChannel) {
			t.Fatalf("NormalisePaymentChannel(%q) error = %v, want ErrUnsupportedPaymentChannel", input, err)
		}
	}
}
