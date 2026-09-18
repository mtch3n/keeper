package catalog

import (
	"testing"

	"github.com/mtchen/keeper/internal/ports"
)

func TestFamilyAssignmentByOID(t *testing.T) {
	cases := []struct {
		oid  uint32
		want ports.TypeFamily
	}{
		// numeric
		{21, ports.FamilyNumeric}, {23, ports.FamilyNumeric}, {20, ports.FamilyNumeric},
		{1700, ports.FamilyNumeric}, {700, ports.FamilyNumeric}, {701, ports.FamilyNumeric}, {790, ports.FamilyNumeric},
		// boolean
		{16, ports.FamilyBoolean},
		// datetime
		{1082, ports.FamilyDateTime}, {1083, ports.FamilyDateTime}, {1266, ports.FamilyDateTime},
		{1114, ports.FamilyDateTime}, {1184, ports.FamilyDateTime}, {1186, ports.FamilyDateTime},
		// uuid
		{2950, ports.FamilyUUID},
		// text (including json/jsonb/xml, which share PostgreSQL's typcategory
		// 'U' with uuid and bytea — SPEC R7.6c is that a category test must not
		// be used here)
		{25, ports.FamilyText}, {1043, ports.FamilyText}, {1042, ports.FamilyText}, {19, ports.FamilyText},
		{18, ports.FamilyText}, {114, ports.FamilyText}, {3802, ports.FamilyText}, {142, ports.FamilyText},
		// unknown: arrays, domains (pre-resolution) and anything else
		{99999, ports.FamilyUnknown},
		{0, ports.FamilyUnknown},
	}
	for _, tc := range cases {
		if got := Family(tc.oid); got != tc.want {
			t.Errorf("Family(%d) = %s, want %s", tc.oid, got, tc.want)
		}
	}
}

func TestFamilyNeverUsesCategoryU(t *testing.T) {
	// R7.6c's specific failure shape: json_agg (jsonb, 3802) must not land in
	// the same family as uuid (2950) or bytea, even though PostgreSQL puts
	// json, jsonb, uuid, bytea and xml all in typcategory 'U'.
	if Family(3802) == Family(2950) {
		t.Errorf("jsonb and uuid must not share a family (R7.6c)")
	}
	if Family(114) == Family(2950) {
		t.Errorf("json and uuid must not share a family (R7.6c)")
	}
}

func TestIsJSONType(t *testing.T) {
	for _, oid := range []uint32{114, 3802} {
		if !isJSONType(oid) {
			t.Errorf("isJSONType(%d) = false, want true", oid)
		}
	}
	for _, oid := range []uint32{25, 2950, 142} {
		if isJSONType(oid) {
			t.Errorf("isJSONType(%d) = true, want false", oid)
		}
	}
}
