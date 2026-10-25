package interpreteval

import (
	"os"
	"strings"
	"testing"
)

// The committed dataset must load: every label valid against the real rule
// validator, so a real run never spends money on a broken case (D75).
func TestParse_CommittedDataset(t *testing.T) {
	data, err := os.ReadFile("../../evals/interpret/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	ds, err := Parse(data)
	if err != nil {
		t.Fatalf("dataset invalid:\n%v", err)
	}
	if n := len(ds.Cases); n < 45 || n > 70 {
		t.Errorf("want about 50 cases, got %d", n)
	}
	langs, tags := map[string]int{}, map[string]int{}
	for _, c := range ds.Cases {
		langs[c.Lang]++
		for _, tag := range c.Tags {
			tags[tag]++
		}
	}
	if langs["en"] < 20 || langs["tr"] < 20 {
		t.Errorf("want both languages well covered, got %v", langs)
	}
	for _, tag := range []string{"single", "multi", "boundary", "unsupported", "ambiguity", "injection", "everyone"} {
		if tags[tag] < 4 {
			t.Errorf("tag %q has %d cases, want at least 4", tag, tags[tag])
		}
	}
}

// Expected rules are stored in canonical form, so comparison later is exact.
func TestParse_NormalizesExpectedRules(t *testing.T) {
	ds, err := Parse([]byte(dataset(`{"id":"a","lang":"en","tags":["single"],"brief":"b",
		"rules":[{"field":"city","op":"in","value":["istanbul"]}]}`)))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(ds.Cases[0].Rules[0].Value); got != `["İstanbul"]` {
		t.Errorf("want canonical city, got %s", got)
	}
}

func TestParse_RejectsBadCases(t *testing.T) {
	ok := `{"id":"a","lang":"en","tags":["single"],"brief":"b","unsupported":true}`
	tests := map[string]struct{ cases, want string }{
		"duplicate id":   {ok + "," + ok, `duplicate id "a"`},
		"no expectation": {`{"id":"a","lang":"en","tags":["single"],"brief":"b"}`, "exactly one of"},
		"both":           {`{"id":"a","lang":"en","tags":["single"],"brief":"b","unsupported":true,"rules":[{"field":"order_count","op":"gte","value":1}]}`, "exactly one of"},
		"invalid rule":   {`{"id":"a","lang":"en","tags":["single"],"brief":"b","rules":[{"field":"city","op":"in","value":["Paris"]}]}`, `unknown city "Paris"`},
		"unknown lang":   {`{"id":"a","lang":"de","tags":["single"],"brief":"b","unsupported":true}`, "lang"},
		"empty brief":    {`{"id":"a","lang":"en","tags":["single"],"brief":" ","unsupported":true}`, "brief"},
		"long brief":     {`{"id":"a","lang":"en","tags":["single"],"brief":"` + strings.Repeat("ş", 1001) + `","unsupported":true}`, "brief"},
		"no tags":        {`{"id":"a","lang":"en","tags":[],"brief":"b","unsupported":true}`, "tag"},
		"unknown field":  {`{"id":"a","lang":"en","tags":["single"],"brief":"b","unsupported":true,"expected":1}`, "unknown field"},
		"missing id":     {`{"lang":"en","tags":["single"],"brief":"b","unsupported":true}`, "id"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(dataset(tc.cases)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// Every problem is reported at once, so one fix round covers the whole file.
func TestParse_ReportsAllProblems(t *testing.T) {
	_, err := Parse([]byte(dataset(`{"id":"a","lang":"xx","tags":[],"brief":"","unsupported":true}`)))
	for _, want := range []string{"lang", "tag", "brief"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want error mentioning %q, got %v", want, err)
		}
	}
}

func TestParse_RejectsBadClockOrZone(t *testing.T) {
	for _, head := range []string{
		`"now":"2026-10-26T10:00:00+03:00","time_zone":"Mars/Base"`,
		`"now":"2026-10-26T10:00:00+03:00","time_zone":""`,
		`"time_zone":"Europe/Istanbul"`,
	} {
		data := `{` + head + `,"catalog":{"cities":["İstanbul"],"categories":["books"]},"cases":[]}`
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("%s: want error", head)
		}
	}
}

func dataset(cases string) string {
	return `{"now":"2026-10-26T10:00:00+03:00","time_zone":"Europe/Istanbul",
		"catalog":{"cities":["İstanbul","Ankara"],"categories":["books"]},"cases":[` + cases + `]}`
}
