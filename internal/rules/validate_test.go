package rules

import "testing"

func TestLuhnKnownVectors(t *testing.T) {
	// Published test numbers from the card schemes' own documentation.
	good := []string{
		"4111111111111111", // Visa
		"4012888888881881", // Visa
		"4222222222222",    // Visa, 13 digits
		"5555555555554444", // Mastercard
		"5105105105105100", // Mastercard
		"2223003122003222", // Mastercard, 2-series
		"378282246310005",  // Amex
		"371449635398431",  // Amex
		"6011111111111117", // Discover
		"6011000990139424", // Discover
		"3530111333300000", // JCB
		"30569309025904",   // Diners
		"6200000000000005", // UnionPay
		"79927398713",      // the canonical Luhn example
	}
	for _, d := range good {
		if !Luhn(d) {
			t.Errorf("Luhn(%s) = false, want true", d)
		}
	}
	bad := []string{
		"4111111111111112", "5555555555554443", "378282246310006",
		"79927398710", "79927398711", "79927398712", "79927398714",
		"1234567812345678", "0000000000000001",
	}
	for _, d := range bad {
		if Luhn(d) {
			t.Errorf("Luhn(%s) = true, want false", d)
		}
	}
	if Luhn("") || Luhn("4") || Luhn("41x1") {
		t.Error("Luhn accepted a non-number")
	}
}

func TestValidCard(t *testing.T) {
	if brand, ok := ValidCard("4111111111111111"); !ok || brand != "visa" {
		t.Errorf("ValidCard visa = %q,%v", brand, ok)
	}
	if brand, ok := ValidCard("378282246310005"); !ok || brand != "amex" {
		t.Errorf("ValidCard amex = %q,%v", brand, ok)
	}
	// Luhn-valid but no issuer range owns a 16-digit number starting 1.
	if _, ok := ValidCard("1234567812345670"); ok {
		t.Error("ValidCard accepted an unassigned issuer range")
	}
	// Right issuer range, wrong length for it.
	if _, ok := ValidCard("51051051051051009"); ok {
		t.Error("ValidCard accepted a mastercard of the wrong length")
	}
}

func TestIBANMod97KnownVectors(t *testing.T) {
	// Registry examples from ISO 13616 / the national central banks.
	good := []string{
		"GB82WEST12345698765432",
		"DE89370400440532013000",
		"FR1420041010050500013M02606",
		"NL91ABNA0417164300",
		"CH9300762011623852957",
		"ES9121000418450200051332",
		"IT60X0542811101000000123456",
		"BE68539007547034",
		"SE4550000000058398257466",
		"NO9386011117947",
		"GB82 WEST 1234 5698 7654 32",
		"gb82west12345698765432",
	}
	for _, s := range good {
		if !ValidIBAN(s) {
			t.Errorf("ValidIBAN(%q) = false, want true", s)
		}
	}
	bad := []string{
		"GB82WEST12345698765433", // check digits wrong
		"GB83WEST12345698765432",
		"DE89370400440532013001",
		"GB82WEST123456987654",   // wrong length for GB
		"DE89370400440532013",    // wrong length for DE
		"1B82WEST12345698765432", // country code not alpha
		"GBX2WEST12345698765432", // check digits not numeric
		"",
		"GB82",
	}
	for _, s := range bad {
		if ValidIBAN(s) {
			t.Errorf("ValidIBAN(%q) = true, want false", s)
		}
	}
}

func TestValidSSN(t *testing.T) {
	good := []string{"123456789", "078051120", "219099999"}
	for _, s := range good {
		if !ValidSSN(s) {
			t.Errorf("ValidSSN(%q) = false, want true", s)
		}
	}
	bad := []string{"000123456", "666123456", "900123456", "123006789", "123450000", "12345678", "1234567890", "12345678a"}
	for _, s := range bad {
		if ValidSSN(s) {
			t.Errorf("ValidSSN(%q) = true, want false", s)
		}
	}
}
