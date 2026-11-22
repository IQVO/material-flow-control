package a2a

import (
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// carry (Task 21 review): decodeStrict accepted a stray closing delimiter after the document (dec.More is false
// on `}` and `]`) and encoding/json's case-insensitive key match (`StationID` filled stationId). Both are refused.
func TestStrictDecodingRefusesTrailingDelimitersAndCaseFoldedKeys(t *testing.T) {
	client := agent(t, fakeReads{})
	for name, c := range map[string]struct {
		part *a2a.Part
		want string
	}{
		"a trailing }":              {a2a.NewTextPart(`{"skill":"station_queue","arguments":{"stationId":"ST1"}}}`), "malformed-skill-request"},
		"a trailing ]":              {a2a.NewTextPart(`{"skill":"station_queue","arguments":{"stationId":"ST1"}}]`), "malformed-skill-request"},
		"a case-folded argument":    {skill("station_queue", map[string]any{"StationID": "ST1"}), "invalid-argument"},
		"a case-folded request key": {a2a.NewDataPart(map[string]any{"Skill": "station_queue", "arguments": map[string]any{"stationId": "ST1"}}), "malformed-skill-request"},
		"case-folded arguments key": {a2a.NewDataPart(map[string]any{"skill": "station_queue", "Arguments": map[string]any{"stationId": "ST1"}}), "malformed-skill-request"},
	} {
		if env, _ := send(t, client, c.part); problemCode(env) != c.want || env["data"] != nil {
			t.Errorf("%s: envelope %v, want only the %s problem", name, env, c.want)
		}
	}
}

func TestStrictDecodingStillAcceptsTheExactKeys(t *testing.T) {
	for name, raw := range map[string]string{
		"exact keys":         `{"stationId":"ST1"}`,
		"trailing blanks":    "{\"stationId\":\"ST1\"} \n\t",
		"empty arguments":    ``,
		"null arguments":     `null`,
		"an empty object":    `{}`,
		"insignificant keys": `{ "stationId" : "ST1" }`,
	} {
		var args stationArgs
		if err := decodeStrict([]byte(raw), &args); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
