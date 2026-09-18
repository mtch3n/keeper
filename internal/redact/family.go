package redact

import (
	"strings"

	"github.com/mtchen/keeper/internal/ports"
)

// Family maps a PostgreSQL type name to the family R7.6 and R8.4b split on.
//
// R7.6c is explicit that the families are an enumerated list and never
// typcategory: PostgreSQL puts uuid, json, jsonb, bytea and xml all in category
// U, so a typcategory test would place json_agg in the uuid family. This table
// enumerates the same set by name; internal/pgdb enumerates it by OID, which is
// the same list reached from the other end. ports.ColumnMeta carries a type
// name and not an OID, which is why this exists.
//
// An unrecognised type — a domain, an enum, a composite, an extension type —
// reports FamilyUnknown, which inherits under both rules. That is the safe
// direction: an unknown type that can hold text and does not inherit is a leak,
// and an unknown type that cannot and does inherit is a masked column.
func Family(typeName string) ports.TypeFamily {
	t := strings.ToLower(strings.TrimSpace(typeName))
	// Array types inherit their element's family: text[] can carry a resolved
	// value, bigint[] cannot.
	for strings.HasSuffix(t, "[]") {
		t = strings.TrimSpace(strings.TrimSuffix(t, "[]"))
	}
	for strings.HasPrefix(t, "_") {
		t = t[1:]
	}
	if i := strings.IndexByte(t, '('); i >= 0 { // varchar(64), numeric(10,2)
		t = strings.TrimSpace(t[:i])
	}
	if f, ok := familyByName[t]; ok {
		return f
	}
	return ports.FamilyUnknown
}

var familyByName = map[string]ports.TypeFamily{
	"smallint": ports.FamilyNumeric, "int2": ports.FamilyNumeric,
	"integer": ports.FamilyNumeric, "int": ports.FamilyNumeric, "int4": ports.FamilyNumeric,
	"bigint": ports.FamilyNumeric, "int8": ports.FamilyNumeric,
	"decimal": ports.FamilyNumeric, "numeric": ports.FamilyNumeric,
	"real": ports.FamilyNumeric, "float4": ports.FamilyNumeric,
	"double precision": ports.FamilyNumeric, "float8": ports.FamilyNumeric,
	"money": ports.FamilyNumeric, "oid": ports.FamilyNumeric,
	"smallserial": ports.FamilyNumeric, "serial": ports.FamilyNumeric, "bigserial": ports.FamilyNumeric,

	"boolean": ports.FamilyBoolean, "bool": ports.FamilyBoolean,

	"timestamp": ports.FamilyDateTime, "timestamptz": ports.FamilyDateTime,
	"timestamp with time zone": ports.FamilyDateTime, "timestamp without time zone": ports.FamilyDateTime,
	"date": ports.FamilyDateTime, "time": ports.FamilyDateTime, "timetz": ports.FamilyDateTime,
	"time with time zone": ports.FamilyDateTime, "time without time zone": ports.FamilyDateTime,
	"interval": ports.FamilyDateTime,

	"uuid": ports.FamilyUUID,

	"text": ports.FamilyText, "varchar": ports.FamilyText, "character varying": ports.FamilyText,
	"char": ports.FamilyText, "character": ports.FamilyText, "bpchar": ports.FamilyText,
	"name": ports.FamilyText, "citext": ports.FamilyText,
	"json": ports.FamilyText, "jsonb": ports.FamilyText, "xml": ports.FamilyText,
}

// TextLike reports whether a family can carry a resolved value out of keeper,
// which is the test R8.4b's policy floor and R7.6's text branch both apply.
// Numeric, boolean, date/time and uuid columns cannot: a text email does not fit
// in a bigint, which is why count(*) and an unrelated numeric status need no
// inheritance in order to stay safe.
func TextLike(f ports.TypeFamily) bool {
	return f == ports.FamilyText || f == ports.FamilyUnknown
}
