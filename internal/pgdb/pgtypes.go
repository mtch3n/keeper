package pgdb

import "github.com/mtchen/keeper/internal/ports"

// SPEC R7.6c: the type families are enumerated by type OID, never by
// typcategory. PostgreSQL puts uuid, json, jsonb, bytea and xml all in category
// U, so a typcategory test would place json_agg in the uuid family and let a
// text expression inherit from nothing.
//
// The table below is the single source of truth for both halves of that answer:
// the OID a RowDescription carries, and the canonical type name that reaches
// types.ColumnMeta.Type. Everything absent from it is FamilyUnknown, which takes
// R7.6's broad branch — the conservative side.
type pgType struct {
	oid    uint32
	name   string
	family ports.TypeFamily
}

var knownTypes = []pgType{
	// boolean
	{16, "bool", ports.FamilyBoolean},
	{1000, "_bool", ports.FamilyBoolean},

	// numeric
	{20, "int8", ports.FamilyNumeric},
	{21, "int2", ports.FamilyNumeric},
	{23, "int4", ports.FamilyNumeric},
	{26, "oid", ports.FamilyNumeric},
	{700, "float4", ports.FamilyNumeric},
	{701, "float8", ports.FamilyNumeric},
	{790, "money", ports.FamilyNumeric},
	{1700, "numeric", ports.FamilyNumeric},
	{1005, "_int2", ports.FamilyNumeric},
	{1007, "_int4", ports.FamilyNumeric},
	{1016, "_int8", ports.FamilyNumeric},
	{1021, "_float4", ports.FamilyNumeric},
	{1022, "_float8", ports.FamilyNumeric},
	{1231, "_numeric", ports.FamilyNumeric},

	// date and time
	{1082, "date", ports.FamilyDateTime},
	{1083, "time", ports.FamilyDateTime},
	{1114, "timestamp", ports.FamilyDateTime},
	{1184, "timestamptz", ports.FamilyDateTime},
	{1186, "interval", ports.FamilyDateTime},
	{1266, "timetz", ports.FamilyDateTime},
	{1115, "_timestamp", ports.FamilyDateTime},
	{1182, "_date", ports.FamilyDateTime},
	{1185, "_timestamptz", ports.FamilyDateTime},

	// uuid
	{2950, "uuid", ports.FamilyUUID},
	{2951, "_uuid", ports.FamilyUUID},

	// text-like. json, jsonb, xml, bytea, inet, cidr and macaddr are here
	// deliberately: every one of them can carry a name, an address or an email
	// as a string leaf, which is what the broad branch of R7.6 is protecting.
	{17, "bytea", ports.FamilyText},
	{18, "char", ports.FamilyText},
	{19, "name", ports.FamilyText},
	{25, "text", ports.FamilyText},
	{114, "json", ports.FamilyText},
	{142, "xml", ports.FamilyText},
	{650, "cidr", ports.FamilyText},
	{829, "macaddr", ports.FamilyText},
	{869, "inet", ports.FamilyText},
	{1042, "bpchar", ports.FamilyText},
	{1043, "varchar", ports.FamilyText},
	{3802, "jsonb", ports.FamilyText},
	{199, "_json", ports.FamilyText},
	{1009, "_text", ports.FamilyText},
	{1015, "_varchar", ports.FamilyText},
	{3807, "_jsonb", ports.FamilyText},
}

var (
	familyByOID  = map[uint32]ports.TypeFamily{}
	familyByName = map[string]ports.TypeFamily{}
	nameByOID    = map[uint32]string{}
)

func init() {
	for _, t := range knownTypes {
		familyByOID[t.oid] = t.family
		familyByName[t.name] = t.family
		nameByOID[t.oid] = t.name
	}
}

// FamilyForOID reports the R7.6 type family of a PostgreSQL type OID. An OID the
// table does not name is FamilyUnknown, which inherits from every non-allow
// column of the referenced relations rather than from its own family only.
func FamilyForOID(oid uint32) ports.TypeFamily {
	if f, ok := familyByOID[oid]; ok {
		return f
	}
	return ports.FamilyUnknown
}

// FamilyForName reports the R7.6 type family of a canonical PostgreSQL type
// name, which is what travels in types.ColumnMeta.Type. It is the same answer
// FamilyForOID gives for the OID that name came from.
func FamilyForName(name string) ports.TypeFamily {
	if f, ok := familyByName[name]; ok {
		return f
	}
	return ports.FamilyUnknown
}

// TextLike reports whether a family can carry a string value, which is the split
// R7.6 and R8.4b both make. Unknown counts as text-like: it is the branch that
// masks more.
func TextLike(f ports.TypeFamily) bool {
	return f == ports.FamilyText || f == ports.FamilyUnknown
}
