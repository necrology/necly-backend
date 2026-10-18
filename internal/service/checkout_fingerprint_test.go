package service

import "testing"

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
