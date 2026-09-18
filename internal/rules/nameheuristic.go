package rules

import (
	"strings"
	"unicode"
)

// Namespaces the name heuristic can propose that no pattern rule detects. They
// are names for classification labels, not claims that keeper can find values
// of that kind in free text — nothing here detects an address or a birth date.
const (
	TypeAddress    = "address"
	TypeDOB        = "dob"
	TypeUsername   = "username"
	TypeTaxID      = "tax_id"
	TypePassport   = "passport"
	TypeLicence    = "licence"
	TypeNationalID = "national_id"
)

// exactColumnRules match the whole normalized column name.
var exactColumnRules = map[string]string{
	"name": TypePersonName, "names": TypePersonName, "fullname": TypePersonName,
	"full_name": TypePersonName, "first_name": TypePersonName, "last_name": TypePersonName,
	"given_name": TypePersonName, "family_name": TypePersonName, "middle_name": TypePersonName,
	"maiden_name": TypePersonName, "surname": TypePersonName, "forename": TypePersonName,
	"firstname": TypePersonName, "lastname": TypePersonName, "middlename": TypePersonName,
	"contact_name": TypePersonName, "person_name": TypePersonName, "patient_name": TypePersonName,
	"customer_name": TypePersonName, "client_name": TypePersonName, "employee_name": TypePersonName,
	"legal_name": TypePersonName, "preferred_name": TypePersonName, "display_name": TypePersonName,
	"fname": TypePersonName, "lname": TypePersonName, "nickname": TypePersonName,

	"email": TypeEmail, "emails": TypeEmail, "email_address": TypeEmail, "e_mail": TypeEmail,
	"mail": TypeEmail, "user_email": TypeEmail, "contact_email": TypeEmail, "email_addr": TypeEmail,

	"phone": TypePhone, "phones": TypePhone, "phone_number": TypePhone, "telephone": TypePhone,
	"tel": TypePhone, "mobile": TypePhone, "mobile_number": TypePhone, "cell": TypePhone,
	"cell_phone": TypePhone, "msisdn": TypePhone, "fax": TypePhone, "contact_number": TypePhone,

	"ssn": TypeUSSSN, "social_security_number": TypeUSSSN, "ssn_num": TypeUSSSN, "sin": TypeUSSSN,

	"card": TypeCard, "card_number": TypeCard, "cardnumber": TypeCard, "credit_card": TypeCard,
	"cc_number": TypeCard, "ccnum": TypeCard, "pan": TypeCard, "card_no": TypeCard,

	"iban": TypeIBAN, "bank_account": TypeIBAN, "account_iban": TypeIBAN,

	"ip": TypeIP, "ip_address": TypeIP, "ipaddr": TypeIP, "ipaddress": TypeIP,
	"client_ip": TypeIP, "remote_ip": TypeIP, "source_ip": TypeIP, "last_login_ip": TypeIP,

	"address": TypeAddress, "address1": TypeAddress, "address2": TypeAddress,
	"address_line1": TypeAddress, "address_line2": TypeAddress, "street": TypeAddress,
	"street1": TypeAddress, "street2": TypeAddress, "street_address": TypeAddress,
	"postal_code": TypeAddress, "postcode": TypeAddress, "zip": TypeAddress, "zipcode": TypeAddress,
	"zip_code": TypeAddress, "home_address": TypeAddress, "billing_address": TypeAddress,
	"shipping_address": TypeAddress,

	"dob": TypeDOB, "date_of_birth": TypeDOB, "birth_date": TypeDOB, "birthdate": TypeDOB,
	"birthday": TypeDOB,

	"username": TypeUsername, "user_name": TypeUsername, "login": TypeUsername,
	"handle": TypeUsername, "screen_name": TypeUsername,

	"tax_id": TypeTaxID, "taxid": TypeTaxID, "tin": TypeTaxID, "vat_number": TypeTaxID,
	"passport": TypePassport, "passport_number": TypePassport,
	"licence": TypeLicence, "license": TypeLicence, "drivers_license": TypeLicence,
	"driver_license": TypeLicence, "license_number": TypeLicence,
	"national_id": TypeNationalID, "nhs_number": TypeNationalID, "nino": TypeNationalID,
	"cpf": TypeNationalID, "cnpj": TypeNationalID,
}

// tokenColumnRules match any single token of the column name. Only labels whose
// word is unambiguous on its own belong here: "name" does not, because
// filename, hostname and tablename all contain it, and "ip" does not, because
// zip and recipient do.
var tokenColumnRules = map[string]string{
	"email": TypeEmail, "emails": TypeEmail,
	"ssn": TypeUSSSN, "msisdn": TypePhone, "iban": TypeIBAN,
	"dob": TypeDOB, "birthdate": TypeDOB,
	"passport": TypePassport, "surname": TypePersonName, "forename": TypePersonName,
}

// nameQualifiers are the words that make a trailing "name" a person's name.
var nameQualifiers = map[string]bool{
	"first": true, "last": true, "full": true, "given": true, "family": true,
	"middle": true, "maiden": true, "contact": true, "person": true, "patient": true,
	"customer": true, "client": true, "employee": true, "legal": true, "preferred": true,
	"display": true, "nick": true, "user": true, "owner": true, "author": true,
	"recipient": true, "guardian": true, "member": true, "subscriber": true,
}

// NameHeuristic reports the PII namespace a column name suggests, for SPEC
// §5.4's "text/varchar matching a PII name heuristic → token" default and
// R5.3's initial classification.
//
// It is a proposal a human reviews (R5.3), not a classification. It is
// deliberately conservative about the word "name": a bare "name" token only
// counts when it is the whole column name or is qualified by a word that makes
// it a person's, so filename, hostname, tablename, product_name and
// company_name do not propose person_name.
//
// The namespace it returns is a classification label, exactly as R8.3 requires:
// two columns whose names both resolve to "email" share a namespace and still
// join, and neither is salted with its own column name.
func NameHeuristic(column string) (namespace string, ok bool) {
	norm := normalizeColumnName(column)
	if norm == "" {
		return "", false
	}
	if ns, hit := exactColumnRules[norm]; hit {
		return ns, true
	}
	tokens := strings.Split(norm, "_")
	for _, t := range tokens {
		if ns, hit := tokenColumnRules[t]; hit {
			return ns, true
		}
	}
	// "…_name" with a qualifier that makes it a person's name.
	for i, t := range tokens {
		if t != "name" && t != "names" {
			continue
		}
		if i > 0 && nameQualifiers[tokens[i-1]] {
			return TypePersonName, true
		}
	}
	// A trailing "_email"/"_phone"/"_address" etc. on any prefix.
	if len(tokens) > 1 {
		if ns, hit := exactColumnRules[tokens[len(tokens)-1]]; hit && ns != TypePersonName {
			return ns, true
		}
		last2 := tokens[len(tokens)-2] + "_" + tokens[len(tokens)-1]
		if ns, hit := exactColumnRules[last2]; hit {
			return ns, true
		}
	}
	return "", false
}

// normalizeColumnName lowercases, splits camelCase and collapses separators, so
// "emailAddress", "EmailAddress" and "email_address" are one key.
func normalizeColumnName(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	prevLower := false
	for _, r := range s {
		switch {
		case r == '_' || r == '-' || r == ' ' || r == '.':
			b.WriteByte('_')
			prevLower = false
		case unicode.IsUpper(r):
			if prevLower {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			prevLower = false
		default:
			b.WriteRune(unicode.ToLower(r))
			prevLower = unicode.IsLower(r) || unicode.IsDigit(r)
		}
	}
	out := b.String()
	for strings.Contains(out, "__") {
		out = strings.ReplaceAll(out, "__", "_")
	}
	return strings.Trim(out, "_")
}
