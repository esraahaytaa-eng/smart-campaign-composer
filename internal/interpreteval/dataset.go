// Package interpreteval scores the AI's reading of campaign briefs against
// labeled cases (D75). Real Claude runs are opt-in (make eval); this package
// itself never calls the API.
package interpreteval

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"smart-campaign-composer/internal/audience"
)

// maxBriefChars mirrors the brief limit (D15): a longer brief would fail
// before any AI call and score as an error, not as an interpretation.
const maxBriefChars = 1000

// Dataset is the labeled cases plus the fixed context every case runs in,
// so a rerun sends the same prompt.
type Dataset struct {
	Now      time.Time        `json:"now"`
	TimeZone string           `json:"time_zone"`
	Catalog  audience.Catalog `json:"catalog"`
	Cases    []Case           `json:"cases"`
}

// Case expects either Unsupported or Rules (canonical after Parse).
type Case struct {
	ID          string          `json:"id"`
	Lang        string          `json:"lang"`
	Tags        []string        `json:"tags"`
	Brief       string          `json:"brief"`
	Unsupported bool            `json:"unsupported,omitempty"`
	Rules       []audience.Rule `json:"rules,omitempty"`
	Note        string          `json:"note,omitempty"` // why the label is what it is
}

// Parse decodes a dataset strictly and checks every case, reporting all
// problems at once. Expected rules are replaced by their canonical form.
func Parse(data []byte) (Dataset, error) {
	var ds Dataset
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ds); err != nil {
		return Dataset{}, fmt.Errorf("decode dataset: %w", err)
	}
	var problems []string
	if ds.Now.IsZero() {
		problems = append(problems, "now is missing")
	}
	if _, err := time.LoadLocation(ds.TimeZone); ds.TimeZone == "" || err != nil {
		problems = append(problems, fmt.Sprintf("time_zone %q is not an IANA zone", ds.TimeZone))
	}
	seen := map[string]bool{}
	for i := range ds.Cases {
		c := &ds.Cases[i]
		bad := func(format string, args ...any) {
			problems = append(problems, fmt.Sprintf("case %d %q: %s", i, c.ID, fmt.Sprintf(format, args...)))
		}
		switch {
		case c.ID == "":
			bad("missing id")
		case seen[c.ID]:
			bad("duplicate id %q", c.ID)
		}
		seen[c.ID] = true
		if c.Lang != "en" && c.Lang != "tr" {
			bad("lang must be en or tr, got %q", c.Lang)
		}
		if len(c.Tags) == 0 {
			bad("needs at least one tag")
		}
		if n := utf8.RuneCountInString(strings.TrimSpace(c.Brief)); n == 0 || n > maxBriefChars {
			bad("brief must be 1 to %d characters", maxBriefChars)
		}
		if c.Unsupported == (len(c.Rules) > 0) {
			bad("needs exactly one of unsupported or rules")
			continue
		}
		if c.Unsupported {
			continue
		}
		rules, errs := audience.Validate(c.Rules, ds.Catalog)
		for _, e := range errs {
			bad("rule %d: %s", e.Rule, e.Message)
		}
		c.Rules = rules
	}
	if len(problems) > 0 {
		return Dataset{}, errors.New(strings.Join(problems, "\n"))
	}
	return ds, nil
}
