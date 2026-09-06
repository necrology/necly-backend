package service

import "testing"

func TestPasswordHasherHashesAndVerifiesWithoutStoringPlaintext(t *testing.T) {
	hasher := NewPasswordHasher(4)
	hash, err := hasher.Hash("CorrectHorseBatteryStaple!")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if hash == "CorrectHorseBatteryStaple!" {
		t.Fatal("Hash() returned plaintext")
	}
	if !hasher.Verify(hash, "CorrectHorseBatteryStaple!") {
		t.Fatal("Verify() rejected the correct password")
	}
	if hasher.Verify(hash, "wrong-password") {
		t.Fatal("Verify() accepted a wrong password")
	}
}
