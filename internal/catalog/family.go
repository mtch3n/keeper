package catalog

import "github.com/mtchen/keeper/internal/ports"

// familyByOID assigns each PostgreSQL scalar type OID keeper cares about to
// the type family SPEC R7.6 inherits over. This is an explicit, closed list
// rather than a typcategory test: CONTRACT.md and SPEC R7.6c call out that
// PostgreSQL puts uuid, json, jsonb, bytea and xml all in category 'U', which
// would place json_agg output in the uuid family.
var familyByOID = map[uint32]ports.TypeFamily{
	// numeric
	21:   ports.FamilyNumeric, // int2
	23:   ports.FamilyNumeric, // int4
	20:   ports.FamilyNumeric, // int8
	1700: ports.FamilyNumeric, // numeric
	700:  ports.FamilyNumeric, // float4
	701:  ports.FamilyNumeric, // float8
	790:  ports.FamilyNumeric, // money

	// boolean
	16: ports.FamilyBoolean, // bool

	// datetime
	1082: ports.FamilyDateTime, // date
	1083: ports.FamilyDateTime, // time
	1266: ports.FamilyDateTime, // timetz
	1114: ports.FamilyDateTime, // timestamp
	1184: ports.FamilyDateTime, // timestamptz
	1186: ports.FamilyDateTime, // interval

	// uuid
	2950: ports.FamilyUUID, // uuid

	// text
	25:   ports.FamilyText, // text
	1043: ports.FamilyText, // varchar
	1042: ports.FamilyText, // bpchar
	19:   ports.FamilyText, // name
	18:   ports.FamilyText, // char
	114:  ports.FamilyText, // json
	3802: ports.FamilyText, // jsonb
	142:  ports.FamilyText, // xml
}

// jsonTypeOIDs are the FamilyText members that are documents rather than
// scalars. SPEC §5.4 defaults them to scan "until paths are catalogued",
// which init.go treats separately from ordinary text/varchar columns even
// though both share FamilyText for R7.6 inheritance purposes.
var jsonTypeOIDs = map[uint32]bool{
	114:  true, // json
	3802: true, // jsonb
}

// isJSONType reports whether typeOID is json or jsonb.
func isJSONType(typeOID uint32) bool { return jsonTypeOIDs[typeOID] }

// Family reports the type family a PostgreSQL type OID belongs to, for SPEC
// R7.6's type-family inheritance. Everything not in the explicit list above
// — including arrays, domains and every other extension or composite type —
// is FamilyUnknown. A domain must be resolved to its base type OID before
// reaching this function; only a live connection to pg_type can do that
// resolution, so it is expected of whatever populates ports.Column.TypeOID
// (internal/pgdb's Introspect), not of this package.
func Family(typeOID uint32) ports.TypeFamily {
	if f, ok := familyByOID[typeOID]; ok {
		return f
	}
	return ports.FamilyUnknown
}
